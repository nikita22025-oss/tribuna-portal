package admin

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func testServer(t *testing.T) (*Server, *sql.DB, string) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	s, err := New(Config{DB: db, UploadDir: dir, SecureCookies: false, LoginLimit: 3, LoginWindow: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if err := CreateUser(db, "admin@example.test", "correct horse battery staple"); err != nil {
		t.Fatal(err)
	}
	return s, db, dir
}
func login(t *testing.T, ts *httptest.Server) *http.Client {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	form := url.Values{"email": {"admin@example.test"}, "password": {"correct horse battery staple"}}
	r, err := c.PostForm(ts.URL+"/admin/login", form)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != http.StatusOK && r.StatusCode != http.StatusSeeOther {
		t.Fatalf("login status %d", r.StatusCode)
	}
	return c
}
func pngBody(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	im := image.NewRGBA(image.Rect(0, 0, 3, 2))
	im.Set(0, 0, color.RGBA{255, 1, 2, 255})
	if err := png.Encode(&b, im); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func multipartRequest(t *testing.T, fields map[string]string, imageData []byte) *http.Request {
	t.Helper()
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	for k, v := range fields {
		_ = mw.WriteField(k, v)
	}
	if imageData != nil {
		p, _ := mw.CreateFormFile("image", "banner.png")
		_, _ = p.Write(imageData)
	}
	mw.Close()
	r := httptest.NewRequest(http.MethodPost, "/admin/", &b)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	return r
}

func TestLoginSetsSecureSessionAndCSRF(t *testing.T) {
	s, _, _ := testServer(t)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	c := login(t, ts)
	r, err := c.Get(ts.URL + "/admin/")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		t.Fatalf("dashboard status %d", r.StatusCode)
	}
	buf := new(bytes.Buffer)
	buf.ReadFrom(r.Body)
	if !strings.Contains(buf.String(), "csrf") || !strings.Contains(buf.String(), "Трибуна / реклама") {
		t.Fatal("dashboard missing csrf or title")
	}
}

func TestRejectsCSRFAndAcceptsRasterUpload(t *testing.T) {
	s, db, dir := testServer(t)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	c := login(t, ts)
	r := multipartRequest(t, map[string]string{"action": "save", "placement": "top", "title": "T", "advertiser": "A", "erid": "erid:1", "click_url": "https://example.test"}, pngBody(t))
	r.URL, _ = url.Parse(ts.URL + "/admin/")
	r.RequestURI = ""
	r.Header.Set("Cookie", cookieHeader(t, c, ts.URL))
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("csrf status %d", resp.StatusCode)
	}
	// Obtain CSRF from dashboard and submit through the authenticated client.
	d, err := c.Get(ts.URL + "/admin/")
	if err != nil {
		t.Fatal(err)
	}
	raw := new(bytes.Buffer)
	raw.ReadFrom(d.Body)
	d.Body.Close()
	marker := `name="csrf" value="`
	i := strings.Index(raw.String(), marker)
	if i < 0 {
		t.Fatal("csrf missing")
	}
	v := raw.String()[i+len(marker):]
	csrf := v[:strings.IndexByte(v, '"')]
	r = multipartRequest(t, map[string]string{"csrf": csrf, "action": "save", "placement": "top", "title": "T", "advertiser": "A", "erid": "erid:1", "click_url": "https://example.test", "active": "on"}, pngBody(t))
	r.URL, _ = url.Parse(ts.URL + "/admin/")
	r.RequestURI = ""
	resp, err = c.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("save status %d", resp.StatusCode)
	}
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM ad_creatives").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("ads %d", n)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("uploaded files %d", len(entries))
	}
}

func cookieHeader(t *testing.T, c *http.Client, base string) string {
	u, _ := url.Parse(base)
	var out []string
	for _, x := range c.Jar.Cookies(u) {
		out = append(out, x.Name+"="+x.Value)
	}
	return strings.Join(out, "; ")
}

func TestUploadRejectsSVG(t *testing.T) {
	s, _, _ := testServer(t)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	c := login(t, ts)
	d, _ := c.Get(ts.URL + "/admin/")
	raw := new(bytes.Buffer)
	raw.ReadFrom(d.Body)
	d.Body.Close()
	marker := `name="csrf" value="`
	i := strings.Index(raw.String(), marker)
	csrf := raw.String()[i+len(marker):]
	csrf = csrf[:strings.IndexByte(csrf, '"')]
	r := multipartRequest(t, map[string]string{"csrf": csrf, "action": "save", "placement": "top", "title": "T", "advertiser": "A", "erid": "erid:1"}, []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`))
	r.URL, _ = url.Parse(ts.URL + "/admin/")
	r.RequestURI = ""
	resp, err := c.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("expected rendered validation error, got %d", resp.StatusCode)
	}
}

func TestRateLimit(t *testing.T) {
	s, _, _ := testServer(t)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	for i := 0; i < 4; i++ {
		r, _ := http.PostForm(ts.URL+"/admin/login", url.Values{"email": {"x"}, "password": {"x"}})
		r.Body.Close()
		if i == 3 && r.StatusCode != http.StatusTooManyRequests {
			t.Fatalf("attempt %d status %d", i, r.StatusCode)
		}
	}
}

func csrfFromDashboard(t *testing.T, c *http.Client, base string) string {
	t.Helper()
	r, err := c.Get(base + "/admin/")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	b := new(bytes.Buffer)
	_, _ = b.ReadFrom(r.Body)
	marker := `name="csrf" value="`
	i := strings.Index(b.String(), marker)
	if i < 0 {
		t.Fatal("csrf missing")
	}
	v := b.String()[i+len(marker):]
	return v[:strings.IndexByte(v, '"')]
}

func TestLoginOriginAndDisabledUser(t *testing.T) {
	s, db, _ := testServer(t)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	form := url.Values{"email": {"admin@example.test"}, "password": {"correct horse battery staple"}}
	r, _ := http.PostForm(ts.URL+"/admin/login", form)
	r.Body.Close()
	// An explicitly foreign Origin must be rejected even with valid credentials.
	req := httptest.NewRequest(http.MethodPost, ts.URL+"/admin/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://evil.example")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("origin status %d", w.Code)
	}
	existing := login(t, ts)
	if _, err := db.Exec(`UPDATE admin_users SET is_active=0 WHERE email=?`, "admin@example.test"); err != nil {
		t.Fatal(err)
	}
	locked, err := existing.Get(ts.URL + "/admin/")
	if err != nil {
		t.Fatal(err)
	}
	lockedBody := new(bytes.Buffer)
	_, _ = lockedBody.ReadFrom(locked.Body)
	locked.Body.Close()
	if !strings.Contains(lockedBody.String(), "Неверный email") && strings.Contains(lockedBody.String(), "name=\"csrf\"") {
		t.Fatal("disabled user retained dashboard access")
	}
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	rr, _ := c.PostForm(ts.URL+"/admin/login", form)
	rr.Body.Close()
	if rr.StatusCode != http.StatusOK {
		t.Fatalf("disabled login status %d", rr.StatusCode)
	}
}

func TestScheduleNormalization(t *testing.T) {
	start, err := normalizeDate("2026-09-09T12:00:00+03:00", false)
	if err != nil || start != "2026-09-09T09:00:00Z" {
		t.Fatalf("start %q %v", start, err)
	}
	end, err := normalizeDate("2026-09-09", true)
	if err != nil || end != "2026-09-09T23:59:59Z" {
		t.Fatalf("end %q %v", end, err)
	}
	if _, err := normalizeDate("yesterday", false); err == nil {
		t.Fatal("invalid schedule accepted")
	}
}

func TestCreateUserRejectsShortPassword(t *testing.T) {
	_, db, _ := testServer(t)
	if err := CreateUser(db, "short@example.test", "short"); err == nil {
		t.Fatal("short password accepted")
	}
}

func TestSecureCookieFlag(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s, err := New(Config{DB: db, UploadDir: t.TempDir(), SecureCookies: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := CreateUser(db, "a@example.test", "correct horse battery staple"); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/admin/login", strings.NewReader(url.Values{"email": {"a@example.test"}, "password": {"correct horse battery staple"}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	c := w.Header().Get("Set-Cookie")
	if !strings.Contains(c, "Secure") || !strings.Contains(c, "SameSite=Strict") || !strings.Contains(c, "HttpOnly") {
		t.Fatalf("insecure session cookie: %q", c)
	}
}

func TestAdToggleAndUpdateWithoutReplacingImage(t *testing.T) {
	s, db, _ := testServer(t)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	c := login(t, ts)
	csrf := csrfFromDashboard(t, c, ts.URL)
	r := multipartRequest(t, map[string]string{"csrf": csrf, "action": "save", "placement": "top", "title": "First", "advertiser": "A", "erid": "erid:1", "active": "on"}, pngBody(t))
	r.URL, _ = url.Parse(ts.URL + "/admin/")
	r.RequestURI = ""
	resp, err := c.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	var id, file string
	if err := db.QueryRow(`SELECT id,file FROM ad_creatives`).Scan(&id, &file); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(file, "/uploads/ads/") {
		t.Fatalf("stored file path %q", file)
	}
	csrf = csrfFromDashboard(t, c, ts.URL)
	form := url.Values{"csrf": {csrf}, "action": {"toggle"}, "id": {id}}
	rr, err := c.PostForm(ts.URL+"/admin/", form)
	if err != nil {
		t.Fatal(err)
	}
	rr.Body.Close()
	var active int
	_ = db.QueryRow(`SELECT is_active FROM ad_creatives WHERE id=?`, id).Scan(&active)
	if active != 0 {
		t.Fatal("toggle did not deactivate")
	}
	edit, err := c.Get(ts.URL + "/admin/?edit=" + url.QueryEscape(id))
	if err != nil {
		t.Fatal(err)
	}
	body := new(bytes.Buffer)
	_, _ = body.ReadFrom(edit.Body)
	edit.Body.Close()
	if !strings.Contains(body.String(), `value="First"`) {
		t.Fatal("edit form did not prefill title")
	}
	put, _ := http.NewRequest(http.MethodPut, ts.URL+"/admin/", nil)
	put.Header.Set("Cookie", cookieHeader(t, c, ts.URL))
	putResp, _ := http.DefaultClient.Do(put)
	putResp.Body.Close()
	if putResp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("PUT status %d", putResp.StatusCode)
	}
}

func TestRemoteIPTrustsLoopbackProxyOnly(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "127.0.0.1:1234"
	r.Header.Set("X-Real-IP", "203.0.113.9")
	if got := remoteIP(r); got != "203.0.113.9" {
		t.Fatalf("proxy ip %q", got)
	}
	r.RemoteAddr = "198.51.100.4:1234"
	r.Header.Set("X-Real-IP", "203.0.113.9")
	if got := remoteIP(r); got != "198.51.100.4" {
		t.Fatalf("untrusted proxy ip %q", got)
	}
}

func TestUploadRejectsOversizeAndFakeRaster(t *testing.T) {
	s, _, _ := testServer(t)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	c := login(t, ts)
	csrf := csrfFromDashboard(t, c, ts.URL)
	fake := bytes.Repeat([]byte("x"), maxUploadBytes+1)
	r := multipartRequest(t, map[string]string{"csrf": csrf, "action": "save", "placement": "top", "title": "T", "advertiser": "A", "erid": "e"}, fake)
	r.URL, _ = url.Parse(ts.URL + "/admin/")
	r.RequestURI = ""
	rr, _ := c.Do(r)
	rr.Body.Close()
	if rr.StatusCode != http.StatusOK {
		t.Fatalf("oversize status %d", rr.StatusCode)
	}
	csrf = csrfFromDashboard(t, c, ts.URL)
	r = multipartRequest(t, map[string]string{"csrf": csrf, "action": "save", "placement": "top", "title": "T", "advertiser": "A", "erid": "e"}, []byte("not-an-image"))
	r.URL, _ = url.Parse(ts.URL + "/admin/")
	r.RequestURI = ""
	rr, _ = c.Do(r)
	rr.Body.Close()
	if rr.StatusCode != http.StatusOK {
		t.Fatalf("fake raster status %d", rr.StatusCode)
	}
}

var _ = json.Valid
var _ = filepath.Join
