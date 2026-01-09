package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/beevik/etree"
	"github.com/use-go/onvif"
	"github.com/use-go/onvif/event"
	"github.com/use-go/onvif/gosoap"
	"github.com/use-go/onvif/media"
	"github.com/use-go/onvif/networking"
	sdkmedia "github.com/use-go/onvif/sdk/media"
	"github.com/use-go/onvif/xsd"
	xsdonvif "github.com/use-go/onvif/xsd/onvif"
)

func newOnvifDevice(client *http.Client) (*onvif.Device, error) {
	return onvif.NewDevice(onvif.DeviceParams{
		Xaddr:      appConfig.Camera.IP,
		Username:   "",
		Password:   "",
		HttpClient: client,
	})
}

func pollMotion(ctx context.Context, client *http.Client, dev *onvif.Device, onUpdate func(bool, []string)) {
	var (
		endpoint        string
		referenceParams []string
		lastMotion      bool
	)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if endpoint == "" {
			subEndpoint, refParams, err := createSubscription(client, dev)
			if err != nil {
				log.Printf("create pull point subscription error: %v", err)
				time.Sleep(5 * time.Second)
				continue
			}
			endpoint = subEndpoint
			referenceParams = refParams
			log.Printf("subscription endpoint: %s", endpoint)
		}

		body, err := callPullMessages(client, endpoint, referenceParams)
		if err != nil {
			log.Printf("pull messages error: %v", err)
			endpoint = ""
			referenceParams = nil
			time.Sleep(3 * time.Second)
			continue
		}

		motionDetected, hasMotionValue, motionNames := parseMotionSimpleItems(body)
		if hasMotionValue && motionDetected != lastMotion {
			lastMotion = motionDetected
			onUpdate(motionDetected, motionNames)
		}
	}
}

type createPullPointSubscriptionRequest struct {
	XMLName string `xml:"tev:CreatePullPointSubscription"`
}

func createSubscription(client *http.Client, dev *onvif.Device) (string, []string, error) {
	endpoint := getEventEndpoint(dev)
	if endpoint == "" {
		return "", nil, fmt.Errorf("event endpoint not found")
	}

	req := createPullPointSubscriptionRequest{
		XMLName: "tev:CreatePullPointSubscription",
	}

	body, err := xml.MarshalIndent(req, "  ", "    ")
	if err != nil {
		return "", nil, err
	}

	doc := etree.NewDocument()
	if err := doc.ReadFromBytes(body); err != nil {
		return "", nil, err
	}

	soap := gosoap.NewEmptySOAP()
	soap.AddBodyContent(doc.Root())
	soap.AddRootNamespaces(onvif.Xlmns)
	soap.AddAction()
	header := fmt.Sprintf(
		`<wsa:To soap-env:mustUnderstand="true">%s</wsa:To>`+
			`<wsa:Action soap-env:mustUnderstand="true">%s</wsa:Action>`+
			`<wsa:MessageID>%s</wsa:MessageID>`+
			`<wsa:ReplyTo><wsa:Address>%s</wsa:Address></wsa:ReplyTo>`,
		xmlEscape(endpoint),
		"http://www.onvif.org/ver10/events/wsdl/EventPortType/CreatePullPointSubscriptionRequest",
		newMessageID(),
		"http://www.w3.org/2005/08/addressing/anonymous",
	)
	if err := soap.AddStringHeaderContent(header); err != nil {
		return "", nil, err
	}

	if appConfig.UseWSSecurity {
		soap.AddWSSecurity(appConfig.Camera.Username, appConfig.Camera.Password)
	}

	resp, err := networking.SendSoap(client, endpoint, soap.String())
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return "", nil, fmt.Errorf("subscription response status %s: %s", resp.Status, strings.TrimSpace(string(respBody)))
	}

	subscriptionEndpoint, referenceParams := extractSubscriptionInfo(string(respBody))
	if subscriptionEndpoint == "" {
		return "", nil, fmt.Errorf("subscription endpoint not found in response")
	}

	return subscriptionEndpoint, referenceParams, nil
}

func getEventEndpoint(dev *onvif.Device) string {
	if endpoint := dev.GetEndpoint("event"); endpoint != "" {
		return endpoint
	}
	if endpoint := dev.GetEndpoint("events"); endpoint != "" {
		return endpoint
	}
	return "http://" + appConfig.Camera.IP + "/onvif/event_service"
}

func callPullMessages(client *http.Client, endpoint string, referenceParams []string) (string, error) {
	req := event.PullMessages{
		XMLName:      "tev:PullMessages",
		Timeout:      xsd.Duration(appConfig.PullTimeout),
		MessageLimit: xsd.Int(appConfig.MessageLimit),
	}

	body, err := xml.MarshalIndent(req, "  ", "    ")
	if err != nil {
		return "", err
	}

	doc := etree.NewDocument()
	if err := doc.ReadFromBytes(body); err != nil {
		return "", err
	}

	soap := gosoap.NewEmptySOAP()
	soap.AddBodyContent(doc.Root())
	soap.AddRootNamespaces(onvif.Xlmns)
	soap.AddAction()
	header := fmt.Sprintf(
		`<wsa:To soap-env:mustUnderstand="true">%s</wsa:To>`+
			`<wsa:Action soap-env:mustUnderstand="true">%s</wsa:Action>`+
			`<wsa:MessageID>%s</wsa:MessageID>`+
			`<wsa:ReplyTo><wsa:Address>%s</wsa:Address></wsa:ReplyTo>`,
		xmlEscape(endpoint),
		"http://www.onvif.org/ver10/events/wsdl/PullPointSubscription/PullMessagesRequest",
		newMessageID(),
		"http://www.w3.org/2005/08/addressing/anonymous",
	)
	if err := soap.AddStringHeaderContent(header); err != nil {
		return "", err
	}
	for _, param := range referenceParams {
		if err := soap.AddStringHeaderContent(param); err != nil {
			return "", err
		}
	}

	if appConfig.UseWSSecurity {
		soap.AddWSSecurity(appConfig.Camera.Username, appConfig.Camera.Password)
	}

	resp, err := networking.SendSoap(client, endpoint, soap.String())
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("pull messages status %s: %s", resp.Status, strings.TrimSpace(string(respBody)))
	}

	return string(respBody), nil
}

func xmlEscape(value string) string {
	replacer := strings.NewReplacer(
		`&`, "&amp;",
		`<`, "&lt;",
		`>`, "&gt;",
		`"`, "&quot;",
		`'`, "&apos;",
	)
	return replacer.Replace(value)
}

func newMessageID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "urn:uuid:00000000-0000-0000-0000-000000000000"
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("urn:uuid:%s-%s-%s-%s-%s",
		hex.EncodeToString(b[0:4]),
		hex.EncodeToString(b[4:6]),
		hex.EncodeToString(b[6:8]),
		hex.EncodeToString(b[8:10]),
		hex.EncodeToString(b[10:16]),
	)
}

func extractSubscriptionInfo(xmlBody string) (string, []string) {
	doc := etree.NewDocument()
	if err := doc.ReadFromString(xmlBody); err != nil {
		return "", nil
	}

	var referenceParams []string
	for _, elem := range doc.FindElements(".//*") {
		if strings.HasSuffix(elem.Tag, "SubscriptionReference") {
			for _, child := range elem.ChildElements() {
				if strings.HasSuffix(child.Tag, "Address") {
					endpoint := strings.TrimSpace(child.Text())
					for _, ref := range elem.ChildElements() {
						if strings.HasSuffix(ref.Tag, "ReferenceParameters") {
							paramDoc := etree.NewDocument()
							paramDoc.SetRoot(ref.Copy())
							if out, err := paramDoc.WriteToString(); err == nil {
								referenceParams = append(referenceParams, out)
							}
						}
					}
					return endpoint, referenceParams
				}
			}
		}
	}

	return "", nil
}

func parseMotionSimpleItems(xmlBody string) (bool, bool, []string) {
	doc := etree.NewDocument()
	if err := doc.ReadFromString(xmlBody); err != nil {
		return false, false, nil
	}

	var names []string
	hasValue := false
	detected := false

	for _, elem := range doc.FindElements(".//*") {
		if !strings.HasSuffix(elem.Tag, "SimpleItem") {
			continue
		}

		name := ""
		value := ""
		for _, attr := range elem.Attr {
			switch strings.ToLower(attr.Key) {
			case "name":
				name = attr.Value
			case "value":
				value = attr.Value
			}
		}

		if name == "" {
			continue
		}

		lowerName := strings.ToLower(name)
		if !strings.Contains(lowerName, "motion") {
			continue
		}

		hasValue = true
		lowerValue := strings.ToLower(strings.TrimSpace(value))
		if lowerValue == "true" || lowerValue == "1" {
			detected = true
			names = append(names, name)
		}
	}

	return detected, hasValue, names
}

type snapshotter struct {
	client       *http.Client
	snapshotHTTP *http.Client
	dev          *onvif.Device
	mu           sync.Mutex
	url          string
	token        string
}

func newSnapshotter(client *http.Client, dev *onvif.Device) *snapshotter {
	return &snapshotter{
		client: client,
		snapshotHTTP: &http.Client{
			Transport: &digestTransport{
				username: appConfig.Camera.Username,
				password: appConfig.Camera.Password,
				rt:       http.DefaultTransport,
			},
			Timeout: client.Timeout,
		},
		dev: dev,
	}
}

func (s *snapshotter) getSnapshot(ctx context.Context) ([]byte, error) {
	snapshotURL, err := s.getSnapshotURL(ctx)
	if err != nil {
		return nil, err
	}
	data, err := s.fetchSnapshot(ctx, snapshotURL)
	if err == nil {
		return data, nil
	}
	s.clearURL()

	snapshotURL, err = s.getSnapshotURL(ctx)
	if err != nil {
		return nil, err
	}
	return s.fetchSnapshot(ctx, snapshotURL)
}

func (s *snapshotter) getSnapshotURL(ctx context.Context) (string, error) {
	s.mu.Lock()
	if s.url != "" {
		url := s.url
		s.mu.Unlock()
		return url, nil
	}
	s.mu.Unlock()

	token, err := s.getProfileToken(ctx)
	if err != nil {
		return "", err
	}

	resp, err := sdkmedia.Call_GetSnapshotUri(ctx, s.dev, media.GetSnapshotUri{
		XMLName:      "trt:GetSnapshotUri",
		ProfileToken: xsdonvif.ReferenceToken(token),
	})
	if err != nil {
		return "", err
	}
	snapshotURL := string(resp.MediaUri.Uri)
	if snapshotURL == "" {
		return "", fmt.Errorf("snapshot uri empty")
	}

	s.mu.Lock()
	s.url = snapshotURL
	s.mu.Unlock()
	return snapshotURL, nil
}

func (s *snapshotter) getProfileToken(ctx context.Context) (string, error) {
	s.mu.Lock()
	if s.token != "" {
		token := s.token
		s.mu.Unlock()
		return token, nil
	}
	s.mu.Unlock()

	resp, err := sdkmedia.Call_GetProfiles(ctx, s.dev, media.GetProfiles{XMLName: "trt:GetProfiles"})
	if err != nil {
		return "", err
	}
	if len(resp.Profiles) == 0 {
		return "", fmt.Errorf("no media profiles")
	}
	token := string(resp.Profiles[0].Token)
	if token == "" {
		return "", fmt.Errorf("empty profile token")
	}

	s.mu.Lock()
	s.token = token
	s.mu.Unlock()
	return token, nil
}

func (s *snapshotter) fetchSnapshot(ctx context.Context, snapshotURL string) ([]byte, error) {
	resp, err := s.doDigestGet(ctx, snapshotURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("snapshot status %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return io.ReadAll(io.LimitReader(resp.Body, 5<<20))
}

func (s *snapshotter) doDigestGet(ctx context.Context, snapshotURL string) (*http.Response, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", snapshotURL, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0")
	return s.snapshotHTTP.Do(req)
}

func (s *snapshotter) clearURL() {
	s.mu.Lock()
	s.url = ""
	s.mu.Unlock()
}
