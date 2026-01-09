package main

import (
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/textproto"
	"strings"
	"sync"
)

type digestTransport struct {
	username string
	password string
	rt       http.RoundTripper
	mu       sync.Mutex
	nonceCnt map[string]int
}

func (t *digestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.rt == nil {
		t.rt = http.DefaultTransport
	}
	if t.nonceCnt == nil {
		t.nonceCnt = make(map[string]int)
	}

	bodyBytes, err := ensureBodyBuffer(req)
	if err != nil {
		return nil, err
	}

	resp, err := t.rt.RoundTrip(req)
	if err != nil {
		return resp, err
	}
	if resp.StatusCode != http.StatusUnauthorized {
		return resp, nil
	}

	challenge := resp.Header.Get("WWW-Authenticate")
	if !strings.HasPrefix(strings.ToLower(challenge), "digest ") {
		return resp, nil
	}
	_ = resp.Body.Close()

	params := parseDigestChallenge(challenge)
	authHeader, err := t.buildAuthHeader(req, params)
	if err != nil {
		return resp, err
	}
	// no debug output

	retryReq := req.Clone(req.Context())
	retryReq.Header = req.Header.Clone()
	retryReq.Header.Set("Authorization", authHeader)
	retryReq.Body = io.NopCloser(strings.NewReader(string(bodyBytes)))
	retryReq.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader(string(bodyBytes))), nil
	}

	return t.rt.RoundTrip(retryReq)
}

func ensureBodyBuffer(req *http.Request) ([]byte, error) {
	if req.Body == nil {
		return nil, nil
	}
	if req.GetBody != nil {
		rc, err := req.GetBody()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		return io.ReadAll(rc)
	}

	bodyBytes, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	req.Body.Close()
	req.Body = io.NopCloser(strings.NewReader(string(bodyBytes)))
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader(string(bodyBytes))), nil
	}
	return bodyBytes, nil
}

func (t *digestTransport) buildAuthHeader(req *http.Request, params map[string]string) (string, error) {
	realm := params["realm"]
	nonce := params["nonce"]
	qop := params["qop"]
	algorithm := params["algorithm"]
	opaque := params["opaque"]

	if algorithm == "" {
		algorithm = "MD5"
	}
	if strings.ToUpper(algorithm) != "MD5" {
		return "", fmt.Errorf("unsupported digest algorithm: %s", algorithm)
	}

	uri := req.URL.EscapedPath()
	if req.URL.RawQuery != "" {
		uri += "?" + req.URL.RawQuery
	}
	if uri == "" {
		uri = req.URL.Path
	}
	nc := nextDigestNonceCount(nonce)
	cnonce := newCnonce()

	ha1 := md5Hex(fmt.Sprintf("%s:%s:%s", t.username, realm, t.password))
	ha2 := md5Hex(fmt.Sprintf("%s:%s", req.Method, uri))

	var response string
	if qop != "" {
		response = md5Hex(fmt.Sprintf("%s:%s:%s:%s:%s:%s", ha1, nonce, nc, cnonce, qop, ha2))
	} else {
		response = md5Hex(fmt.Sprintf("%s:%s:%s", ha1, nonce, ha2))
	}

	return formatDigestHeader(t.username, realm, nonce, uri, cnonce, nc, algorithm, response, qop, opaque), nil
}

func (t *digestTransport) nextNonceCount(nonce string) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.nonceCnt == nil {
		t.nonceCnt = make(map[string]int)
	}
	t.nonceCnt[nonce]++
	return fmt.Sprintf("%08x", t.nonceCnt[nonce])
}

func parseDigestChallenge(header string) map[string]string {
	params := make(map[string]string)
	parts := strings.SplitN(header, " ", 2)
	if len(parts) < 2 {
		return params
	}
	s := parts[1]

	for len(s) > 0 {
		s = strings.TrimLeft(s, " ,")
		if s == "" {
			break
		}
		eq := strings.IndexByte(s, '=')
		if eq < 0 {
			break
		}
		key := textproto.CanonicalMIMEHeaderKey(strings.TrimSpace(s[:eq]))
		s = s[eq+1:]
		if len(s) == 0 {
			break
		}
		var val string
		if s[0] == '"' {
			s = s[1:]
			i := strings.IndexByte(s, '"')
			if i < 0 {
				break
			}
			val = s[:i]
			s = s[i+1:]
		} else {
			i := strings.IndexByte(s, ',')
			if i < 0 {
				val = strings.TrimSpace(s)
				s = ""
			} else {
				val = strings.TrimSpace(s[:i])
				s = s[i+1:]
			}
		}
		params[strings.ToLower(key)] = val
	}

	if qop, ok := params["qop"]; ok {
		if strings.Contains(qop, ",") {
			params["qop"] = strings.TrimSpace(strings.Split(qop, ",")[0])
		}
	}
	return params
}

func md5Hex(value string) string {
	sum := md5.Sum([]byte(value))
	return hex.EncodeToString(sum[:])
}

func newCnonce() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "0000000000000000"
	}
	return hex.EncodeToString(b[:])
}

var digestNcMu sync.Mutex
var digestNonceCnt = make(map[string]int)

func buildDigestAuthHeader(req *http.Request, username, password, challenge string) (string, error) {
	if !strings.HasPrefix(strings.ToLower(challenge), "digest ") {
		return "", fmt.Errorf("unsupported auth challenge")
	}
	params := parseDigestChallenge(challenge)

	realm := params["realm"]
	nonce := params["nonce"]
	qop := params["qop"]
	algorithm := params["algorithm"]
	opaque := params["opaque"]

	if algorithm == "" {
		algorithm = "MD5"
	}
	if strings.ToUpper(algorithm) != "MD5" {
		return "", fmt.Errorf("unsupported digest algorithm: %s", algorithm)
	}

	uri := req.URL.EscapedPath()
	if req.URL.RawQuery != "" {
		uri += "?" + req.URL.RawQuery
	}
	if uri == "" {
		uri = req.URL.Path
	}

	nc := nextDigestNonceCount(nonce)
	cnonce := newCnonce()

	ha1 := md5Hex(fmt.Sprintf("%s:%s:%s", username, realm, password))
	ha2 := md5Hex(fmt.Sprintf("%s:%s", req.Method, uri))

	var response string
	if qop != "" {
		response = md5Hex(fmt.Sprintf("%s:%s:%s:%s:%s:%s", ha1, nonce, nc, cnonce, qop, ha2))
	} else {
		response = md5Hex(fmt.Sprintf("%s:%s:%s", ha1, nonce, ha2))
	}

	return formatDigestHeader(username, realm, nonce, uri, cnonce, nc, algorithm, response, qop, opaque), nil
}

func nextDigestNonceCount(nonce string) string {
	digestNcMu.Lock()
	defer digestNcMu.Unlock()
	digestNonceCnt[nonce]++
	return fmt.Sprintf("%08x", digestNonceCnt[nonce])
}

func formatDigestHeader(username, realm, nonce, uri, cnonce, nc, algorithm, response, qop, opaque string) string {
	parts := []string{
		`username="` + username + `"`,
		`realm="` + realm + `"`,
		`nonce="` + nonce + `"`,
		`uri="` + uri + `"`,
	}
	if cnonce != "" && qop != "" {
		parts = append(parts, `cnonce="`+cnonce+`"`, `nc=`+nc)
	}
	if algorithm != "" {
		parts = append(parts, `algorithm=`+algorithm)
	}
	parts = append(parts, `response="`+response+`"`)
	if qop != "" {
		parts = append(parts, `qop="`+qop+`"`)
	}
	if opaque != "" {
		parts = append(parts, `opaque="`+opaque+`"`)
	}
	return "Digest " + strings.Join(parts, ",")
}
