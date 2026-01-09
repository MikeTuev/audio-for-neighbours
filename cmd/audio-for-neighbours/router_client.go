package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

type routerClient struct {
	base     string
	username string
	password string
	lang     string
	client   *http.Client
	cookie   string
}

func newRouterClient(base, username, password, lang string) *routerClient {
	jar, _ := cookiejar.New(nil)
	return &routerClient{
		base:     strings.TrimRight(base, "/"),
		username: username,
		password: password,
		lang:     lang,
		client: &http.Client{
			Jar:     jar,
			Timeout: 8 * time.Second,
		},
	}
}

func (r *routerClient) fetchOnlineTargets(ctx context.Context, targets []string) ([]string, error) {
	if r.cookie == "" {
		if err := r.login(ctx); err != nil {
			return nil, err
		}
	}

	devices, err := r.fetchUserDevices(ctx)
	if err != nil {
		r.cookie = ""
		if err := r.login(ctx); err != nil {
			return nil, err
		}
		devices, err = r.fetchUserDevices(ctx)
		if err != nil {
			return nil, err
		}
	}

	targetSet := make(map[string]struct{}, len(targets))
	for _, t := range targets {
		targetSet[strings.ToLower(t)] = struct{}{}
	}

	var online []string
	for _, dev := range devices {
		if strings.ToLower(dev.Status) != "online" {
			continue
		}
		name := strings.TrimSpace(dev.HostName)
		if name == "" {
			continue
		}
		if _, ok := targetSet[strings.ToLower(name)]; ok {
			online = append(online, name)
		}
	}
	sort.Strings(online)
	return online, nil
}

func (r *routerClient) login(ctx context.Context) error {
	baseURL, err := url.Parse(r.base)
	if err != nil {
		return err
	}

	req0, _ := http.NewRequestWithContext(ctx, "GET", r.base+"/", nil)
	req0.Header.Set("User-Agent", "Mozilla/5.0")
	resp0, err := r.client.Do(req0)
	if err != nil {
		return err
	}
	body0, _ := io.ReadAll(io.LimitReader(resp0.Body, 2<<20))
	resp0.Body.Close()

	cntStr, err := extractCnt(string(body0))
	if err != nil {
		return err
	}

	tid := buildTid(cntStr, r.username, r.password)
	cookieValue := fmt.Sprintf("tid=%s:Language:%s:id=-1", tid, r.lang)
	r.client.Jar.SetCookies(baseURL, []*http.Cookie{
		{Name: "Cookie", Value: cookieValue, Path: "/"},
	})

	reqL, _ := http.NewRequestWithContext(ctx, "GET", r.base+"/login.cgi", nil)
	reqL.Header.Set("User-Agent", "Mozilla/5.0")
	reqL.Header.Set("Referer", r.base+"/")
	respL, err := r.client.Do(reqL)
	if err != nil {
		return err
	}
	respL.Body.Close()

	for _, c := range r.client.Jar.Cookies(baseURL) {
		if c.Name == "Cookie" && strings.Contains(c.Value, "sid=") {
			r.cookie = c.Value
			return nil
		}
	}
	return fmt.Errorf("sid cookie not found")
}

func (r *routerClient) fetchUserDevices(ctx context.Context) ([]userDevice, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", r.base+"/html/status/userdevinfo.asp", nil)
	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("Cookie", r.cookie)

	resp, err := r.client.Do(req)
	if err != nil {
		return nil, err
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %s", resp.Status)
	}

	return parseUserDevices(string(body)), nil
}

type userDevice struct {
	HostName string
	Status   string
}

var reGetRandCnt = regexp.MustCompile(`GetRandCnt\s*\(\)\s*\{\s*return\s*([0-9]+)\s*;`)
var reUserDevice = regexp.MustCompile(`USERDevice\(([^)]*)\)`)

func extractCnt(html string) (string, error) {
	m := reGetRandCnt.FindStringSubmatch(html)
	if len(m) != 2 {
		return "", fmt.Errorf("cnt not found")
	}
	return m[1], nil
}

func buildTid(cnt, username, password string) string {
	p1 := md5Hex(cnt)
	p2 := md5Hex(username + cnt)
	p3 := md5Hex(md5Hex(password) + cnt)
	return p1 + p2 + p3
}

func parseUserDevices(html string) []userDevice {
	matches := reUserDevice.FindAllStringSubmatch(html, -1)
	var devices []userDevice
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		args := splitJsArgs(match[1])
		if len(args) < 10 {
			continue
		}
		devices = append(devices, userDevice{
			Status:   args[6],
			HostName: args[9],
		})
	}
	return devices
}

func splitJsArgs(input string) []string {
	var args []string
	var b strings.Builder
	inQuote := false
	for i := 0; i < len(input); i++ {
		ch := input[i]
		if ch == '"' {
			inQuote = !inQuote
			continue
		}
		if ch == ',' && !inQuote {
			args = append(args, strings.TrimSpace(b.String()))
			b.Reset()
			continue
		}
		b.WriteByte(ch)
	}
	if b.Len() > 0 {
		args = append(args, strings.TrimSpace(b.String()))
	}
	for i := range args {
		args[i] = strings.TrimSpace(args[i])
	}
	return args
}

func pollPresence(ctx context.Context, client *routerClient, targets []string, onUpdate func([]string)) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			online, err := client.fetchOnlineTargets(ctx, targets)
			if err != nil {
				logPresenceError(err)
				continue
			}
			onUpdate(online)
		}
	}
}

var presenceErrMu sync.Mutex
var lastPresenceErr time.Time

func logPresenceError(err error) {
	presenceErrMu.Lock()
	defer presenceErrMu.Unlock()
	if time.Since(lastPresenceErr) < time.Minute {
		return
	}
	lastPresenceErr = time.Now()
	log.Printf("presence check error: %v", err)
}
