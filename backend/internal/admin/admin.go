// Package admin implements the authenticated advertising administration UI.
package admin

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"errors"
	"html/template"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"log"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const (
	maxUploadBytes = 5 << 20
	maxImageSide   = 5000
	cookieName     = "trbn_admin"
	sessionTTL     = 12 * time.Hour
)

// Config controls an admin server. DB is the database containing admin and ad
// tables (the data Store DB can be supplied by the caller). Refresh is called
// after every ad mutation so the public bundle is regenerated.
type Config struct {
	DB            *sql.DB
	UploadDir     string
	SecureCookies bool
	Refresh       func(context.Context) error
	Logger        *log.Logger
	LoginLimit    int
	LoginWindow   time.Duration
}

type Server struct {
	cfg    Config
	tpl    *template.Template
	mu     sync.Mutex
	failed map[string]attempts
}
type attempts struct {
	at    time.Time
	count int
}

//go:embed templates/*.html
var templateFS embed.FS

func New(cfg Config) (*Server, error) {
	if cfg.DB == nil {
		return nil, errors.New("admin: nil database")
	}
	if cfg.UploadDir == "" {
		return nil, errors.New("admin: empty upload directory")
	}
	if cfg.LoginLimit <= 0 {
		cfg.LoginLimit = 8
	}
	if cfg.LoginWindow <= 0 {
		cfg.LoginWindow = 10 * time.Minute
	}
	if cfg.Logger == nil {
		cfg.Logger = log.Default()
	}
	if err := migrate(cfg.DB); err != nil {
		return nil, err
	}
	t, err := template.ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(cfg.UploadDir, 0o750); err != nil {
		return nil, err
	}
	return &Server{cfg: cfg, tpl: t, failed: make(map[string]attempts)}, nil
}

func migrate(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS admin_users (
 id INTEGER PRIMARY KEY AUTOINCREMENT, email TEXT NOT NULL UNIQUE,
 password_hash TEXT NOT NULL, role TEXT NOT NULL DEFAULT 'manager', is_active INTEGER NOT NULL DEFAULT 1,
 created_at TEXT NOT NULL
); CREATE TABLE IF NOT EXISTS admin_sessions (
 token_hash TEXT PRIMARY KEY, user_id INTEGER NOT NULL, csrf_token TEXT NOT NULL,
 expires_at TEXT NOT NULL, ip TEXT NOT NULL, user_agent TEXT NOT NULL
); CREATE INDEX IF NOT EXISTS admin_sessions_expiry ON admin_sessions(expires_at);
CREATE TABLE IF NOT EXISTS audit_log (
 id INTEGER PRIMARY KEY AUTOINCREMENT, user_id INTEGER, action TEXT NOT NULL,
 object_type TEXT NOT NULL, object_id TEXT NOT NULL, created_at TEXT NOT NULL, ip TEXT NOT NULL
); CREATE TABLE IF NOT EXISTS ad_creatives (
 id TEXT PRIMARY KEY, placement TEXT NOT NULL, title TEXT NOT NULL, advertiser TEXT NOT NULL,
 erid TEXT NOT NULL, click_url TEXT NOT NULL, file TEXT NOT NULL, width INTEGER NOT NULL,
 height INTEGER NOT NULL, is_active INTEGER NOT NULL DEFAULT 1, starts_at TEXT, ends_at TEXT,
 created_at TEXT NOT NULL
);`)
	return err
}

// Handler returns the /admin router. It may be mounted at root or behind a
// reverse proxy that strips /admin; both URL forms are accepted.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/admin/login", s.login)
	mux.HandleFunc("/admin/logout", s.logout)
	mux.HandleFunc("/admin/", s.dashboard)
	mux.HandleFunc("/login", s.login)
	mux.HandleFunc("/logout", s.logout)
	mux.HandleFunc("/", s.dashboard)
	return securityHeaders(mux)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		s.render(w, "login.html", map[string]any{"Error": ""})
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !sameOrigin(r) {
		http.Error(w, "invalid origin", http.StatusForbidden)
		return
	}
	ip := remoteIP(r)
	if !s.allowLogin(ip) {
		http.Error(w, "too many attempts", http.StatusTooManyRequests)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", 400)
		return
	}
	email := strings.ToLower(strings.TrimSpace(r.FormValue("email")))
	password := r.FormValue("password")
	var id int64
	var hash string
	var active int
	err := s.cfg.DB.QueryRow(`SELECT id,password_hash,is_active FROM admin_users WHERE email=?`, email).Scan(&id, &hash, &active)
	if err != nil || active != 1 || bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		s.render(w, "login.html", map[string]any{"Error": "Неверный email или пароль"})
		return
	}
	token, err := randomToken(32)
	if err != nil {
		http.Error(w, "internal error", 500)
		return
	}
	csrf, err := randomToken(32)
	if err != nil {
		http.Error(w, "internal error", 500)
		return
	}
	now := time.Now().UTC()
	_, err = s.cfg.DB.Exec(`INSERT INTO admin_sessions(token_hash,user_id,csrf_token,expires_at,ip,user_agent) VALUES(?,?,?,?,?,?)`, hashToken(token), id, csrf, now.Add(sessionTTL).Format(time.RFC3339), ip, r.UserAgent())
	if err != nil {
		http.Error(w, "internal error", 500)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: token, Path: "/", HttpOnly: true, Secure: s.cfg.SecureCookies, SameSite: http.SameSiteStrictMode, MaxAge: int(sessionTTL.Seconds())})
	s.mu.Lock()
	delete(s.failed, ip)
	s.mu.Unlock()
	http.Redirect(w, r, "/admin/", http.StatusSeeOther)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !sameOrigin(r) {
		http.Error(w, "invalid origin", http.StatusForbidden)
		return
	}
	if sess, ok := s.auth(r); !ok || r.FormValue("csrf") != sess.csrf {
		http.Error(w, "invalid csrf", http.StatusForbidden)
		return
	}
	if c, err := r.Cookie(cookieName); err == nil {
		_, _ = s.cfg.DB.Exec(`DELETE FROM admin_sessions WHERE token_hash=?`, hashToken(c.Value))
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", HttpOnly: true, Secure: s.cfg.SecureCookies, SameSite: http.SameSiteStrictMode, MaxAge: -1})
	http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
}

type session struct {
	userID int64
	csrf   string
}

func (s *Server) auth(r *http.Request) (session, bool) {
	c, err := r.Cookie(cookieName)
	if err != nil || c.Value == "" {
		return session{}, false
	}
	var x session
	var exp string
	err = s.cfg.DB.QueryRow(`SELECT s.user_id,s.csrf_token,s.expires_at FROM admin_sessions s JOIN admin_users u ON u.id=s.user_id AND u.is_active=1 WHERE s.token_hash=?`, hashToken(c.Value)).Scan(&x.userID, &x.csrf, &exp)
	if err != nil {
		return session{}, false
	}
	t, err := time.Parse(time.RFC3339, exp)
	if err != nil || t.Before(time.Now().UTC()) {
		_, _ = s.cfg.DB.Exec(`DELETE FROM admin_sessions WHERE token_hash=?`, hashToken(c.Value))
		return session{}, false
	}
	return x, true
}

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	x, ok := s.auth(r)
	if !ok {
		http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
		return
	}
	if r.Method == http.MethodPost {
		if r.ContentLength > maxUploadBytes+1<<20 {
			http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes+1<<20)
		if err := r.ParseMultipartForm(maxUploadBytes + 1<<20); err != nil && !errors.Is(err, http.ErrNotMultipart) {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if r.MultipartForm != nil {
			defer r.MultipartForm.RemoveAll()
		}
		if r.FormValue("csrf") != x.csrf {
			http.Error(w, "invalid csrf", http.StatusForbidden)
			return
		}
		if err := s.mutateAd(w, r, x); err != nil {
			ads, _ := s.listAds()
			s.renderDashboard(w, x.csrf, err.Error(), formAdFromRequest(r), r.FormValue("id") != "", ads)
			return
		}
		http.Redirect(w, r, "/admin/", http.StatusSeeOther)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ads, err := s.listAds()
	if err != nil {
		http.Error(w, "database error", 500)
		return
	}
	edit := adRow{}
	editing := false
	if id := r.URL.Query().Get("edit"); id != "" {
		for _, a := range ads {
			if a.ID == id {
				edit, editing = a, true
				break
			}
		}
		if !editing {
			http.NotFound(w, r)
			return
		}
	}
	s.renderDashboard(w, x.csrf, "", edit, editing, ads)
}

func (s *Server) renderDashboard(w http.ResponseWriter, csrf, err string, edit adRow, editing bool, ads ...[]adRow) {
	rows := []adRow(nil)
	if len(ads) > 0 {
		rows = ads[0]
	}
	s.render(w, "dashboard.html", map[string]any{"Ads": rows, "CSRF": csrf, "Error": err, "Edit": edit, "Editing": editing})
}

func formAdFromRequest(r *http.Request) adRow {
	return adRow{ID: strings.TrimSpace(r.FormValue("id")), Placement: strings.TrimSpace(r.FormValue("placement")), Title: strings.TrimSpace(r.FormValue("title")), Advertiser: strings.TrimSpace(r.FormValue("advertiser")), ERID: strings.TrimSpace(r.FormValue("erid")), ClickURL: strings.TrimSpace(r.FormValue("click_url")), StartsAt: strings.TrimSpace(r.FormValue("starts_at")), EndsAt: strings.TrimSpace(r.FormValue("ends_at")), Active: r.FormValue("active") == "on" || r.FormValue("active") == "1"}
}

type adRow struct {
	ID, Placement, Title, Advertiser, ERID, ClickURL, File, StartsAt, EndsAt string
	Width, Height                                                            int
	Active                                                                   bool
}

func (s *Server) listAds() ([]adRow, error) {
	rows, err := s.cfg.DB.Query(`SELECT id,placement,title,advertiser,erid,click_url,file,width,height,is_active,COALESCE(starts_at,''),COALESCE(ends_at,'') FROM ad_creatives ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []adRow
	for rows.Next() {
		var a adRow
		var active int
		if err := rows.Scan(&a.ID, &a.Placement, &a.Title, &a.Advertiser, &a.ERID, &a.ClickURL, &a.File, &a.Width, &a.Height, &active, &a.StartsAt, &a.EndsAt); err != nil {
			return nil, err
		}
		a.Active = active == 1
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Server) mutateAd(w http.ResponseWriter, r *http.Request, x session) error {
	action := r.FormValue("action")
	if action == "toggle" {
		id := strings.TrimSpace(r.FormValue("id"))
		if id == "" {
			return errors.New("missing id")
		}
		if _, err := s.cfg.DB.Exec(`UPDATE ad_creatives SET is_active=CASE is_active WHEN 1 THEN 0 ELSE 1 END WHERE id=?`, id); err != nil {
			return err
		}
		s.audit(x.userID, "toggle", "ad", id, r)
		return s.refresh(r.Context())
	}
	if action == "delete" {
		id := r.FormValue("id")
		if id == "" {
			return errors.New("missing id")
		}
		var stored string
		_ = s.cfg.DB.QueryRow(`SELECT file FROM ad_creatives WHERE id=?`, id).Scan(&stored)
		if _, err := s.cfg.DB.Exec(`DELETE FROM ad_creatives WHERE id=?`, id); err != nil {
			return err
		}
		if stored != "" {
			name := strings.TrimPrefix(stored, "/uploads/ads/")
			if name == stored || strings.Contains(name, "..") || strings.ContainsAny(name, `/\\`) {
				name = filepath.Base(stored)
			}
			_ = os.Remove(filepath.Join(s.cfg.UploadDir, name))
		}
		s.audit(x.userID, "delete", "ad", id, r)
		return s.refresh(r.Context())
	}
	if action != "save" {
		return errors.New("unknown action")
	}
	id := strings.TrimSpace(r.FormValue("id"))
	if id == "" {
		id, _ = randomToken(12)
	}
	placement := strings.TrimSpace(r.FormValue("placement"))
	if placement != "top" && placement != "inline" && placement != "sidebar" {
		return errors.New("placement must be top, inline or sidebar")
	}
	title := strings.TrimSpace(r.FormValue("title"))
	advertiser := strings.TrimSpace(r.FormValue("advertiser"))
	erid := strings.TrimSpace(r.FormValue("erid"))
	click := strings.TrimSpace(r.FormValue("click_url"))
	if title == "" || advertiser == "" || erid == "" {
		return errors.New("title, advertiser and ERID are required")
	}
	if click != "" {
		if !strings.HasPrefix(click, "https://") && !strings.HasPrefix(click, "http://") {
			return errors.New("click URL must be http(s)")
		}
	}
	width, height := 0, 0
	var file string
	var oldFile string
	_ = s.cfg.DB.QueryRow(`SELECT file FROM ad_creatives WHERE id=?`, id).Scan(&oldFile)
	file = oldFile
	newName := ""
	committed := false
	defer func() {
		if newName != "" && !committed {
			_ = os.Remove(filepath.Join(s.cfg.UploadDir, newName))
		}
	}()
	if fh, _, err := r.FormFile("image"); err == nil {
		defer fh.Close()
		name, w, h, err := saveRaster(s.cfg.UploadDir, fh)
		if err != nil {
			return err
		}
		file = "/uploads/ads/" + name
		newName = name
		width, height = w, h
	} else if !errors.Is(err, http.ErrMissingFile) {
		return err
	}
	if file == "" {
		return errors.New("raster image is required")
	}
	if width == 0 {
		var err error
		_, width, height, err = inspectRaster(s.cfg.UploadDir, file)
		if err != nil {
			return err
		}
	}
	active := 0
	if r.FormValue("active") == "on" || r.FormValue("active") == "1" {
		active = 1
	}
	starts := strings.TrimSpace(r.FormValue("starts_at"))
	ends := strings.TrimSpace(r.FormValue("ends_at"))
	var dateErr error
	starts, dateErr = normalizeDate(starts, false)
	if dateErr != nil {
		return dateErr
	}
	ends, dateErr = normalizeDate(ends, true)
	if dateErr != nil {
		return dateErr
	}
	if err := validateDate(starts); err != nil {
		return err
	}
	if err := validateDate(ends); err != nil {
		return err
	}
	if starts != "" && ends != "" {
		st, _ := parseDate(starts)
		en, _ := parseDate(ends)
		if st.After(en) {
			return errors.New("start date must not be after end date")
		}
	}
	_, err := s.cfg.DB.Exec(`INSERT INTO ad_creatives(id,placement,title,advertiser,erid,click_url,file,width,height,is_active,starts_at,ends_at,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET placement=excluded.placement,title=excluded.title,advertiser=excluded.advertiser,erid=excluded.erid,click_url=excluded.click_url,file=excluded.file,width=excluded.width,height=excluded.height,is_active=excluded.is_active,starts_at=excluded.starts_at,ends_at=excluded.ends_at`, id, placement, title, advertiser, erid, click, file, width, height, active, starts, ends, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return err
	}
	committed = true
	if newName != "" && oldFile != "" {
		oldName := filepath.Base(oldFile)
		if oldName != "." && oldName != newName {
			_ = os.Remove(filepath.Join(s.cfg.UploadDir, oldName))
		}
	}
	s.audit(x.userID, "save", "ad", id, r)
	return s.refresh(r.Context())
}

func validateDate(v string) error {
	if v == "" {
		return nil
	}
	if _, err := time.Parse(time.RFC3339, v); err != nil {
		if _, e := time.Parse("2006-01-02", v); e != nil {
			return errors.New("date must be RFC3339 or YYYY-MM-DD")
		}
	}
	return nil
}
func parseDate(v string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t, nil
	}
	return time.Parse("2006-01-02", v)
}
func normalizeDate(v string, end bool) (string, error) {
	if v == "" {
		return "", nil
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		t, err = time.Parse("2006-01-02", v)
		if err == nil && end {
			t = t.Add(24*time.Hour - time.Second)
		}
	}
	if err != nil {
		return "", errors.New("date must be RFC3339 or YYYY-MM-DD")
	}
	return t.UTC().Format(time.RFC3339), nil
}
func (s *Server) refresh(ctx context.Context) error {
	if s.cfg.Refresh == nil {
		return nil
	}
	return s.cfg.Refresh(ctx)
}
func (s *Server) audit(uid int64, action, typ, id string, r *http.Request) {
	_, _ = s.cfg.DB.Exec(`INSERT INTO audit_log(user_id,action,object_type,object_id,created_at,ip) VALUES(?,?,?,?,?,?)`, uid, action, typ, id, time.Now().UTC().Format(time.RFC3339), remoteIP(r))
}
func (s *Server) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := s.tpl.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, "template error", 500)
	}
}
func (s *Server) allowLogin(ip string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for key, a := range s.failed {
		if now.Sub(a.at) > s.cfg.LoginWindow {
			delete(s.failed, key)
		}
	}
	if len(s.failed) >= 4096 {
		oldest := ""
		var ot time.Time
		for key, a := range s.failed {
			if oldest == "" || a.at.Before(ot) {
				oldest, ot = key, a.at
			}
		}
		if oldest != "" {
			delete(s.failed, oldest)
		}
	}
	a := s.failed[ip]
	if now.Sub(a.at) > s.cfg.LoginWindow {
		a = attempts{at: now}
	}
	a.count++
	a.at = now
	s.failed[ip] = a
	return a.count <= s.cfg.LoginLimit
}
func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		if forwarded := net.ParseIP(strings.TrimSpace(r.Header.Get("X-Real-IP"))); forwarded != nil {
			return forwarded.String()
		}
	}
	return host
}

// Browsers omit Origin on same-origin form posts in some older clients; when
// present, require an exact origin match to prevent cross-site login/logout.
func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}
func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
func hashToken(v string) string { h := sha256.Sum256([]byte(v)); return hex.EncodeToString(h[:]) }

func saveRaster(dir string, src multipart.File) (string, int, int, error) {
	lr := io.LimitReader(src, maxUploadBytes+1)
	data, err := io.ReadAll(lr)
	if err != nil {
		return "", 0, 0, err
	}
	if len(data) > maxUploadBytes {
		return "", 0, 0, errors.New("image exceeds 5MB")
	}
	cfg, format, err := image.DecodeConfig(strings.NewReader(string(data)))
	if err != nil {
		return "", 0, 0, errors.New("invalid raster image")
	}
	if format != "png" && format != "jpeg" && format != "gif" {
		return "", 0, 0, errors.New("only PNG, JPEG and GIF are allowed")
	}
	if cfg.Width < 1 || cfg.Height < 1 || cfg.Width > maxImageSide || cfg.Height > maxImageSide {
		return "", 0, 0, errors.New("image dimensions exceed 5000px")
	}
	ext := "." + format
	if format == "jpeg" {
		ext = ".jpg"
	}
	name, _ := randomToken(18)
	name += ext
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o640); err != nil {
		return "", 0, 0, err
	}
	return name, cfg.Width, cfg.Height, nil
}
func inspectRaster(dir, stored string) (string, int, int, error) {
	name := strings.TrimPrefix(stored, "/uploads/ads/")
	if name == stored || strings.Contains(name, "..") || strings.ContainsAny(name, `/\\`) {
		name = filepath.Base(stored)
	}
	f, err := os.Open(filepath.Join(dir, name))
	if err != nil {
		return "", 0, 0, err
	}
	defer f.Close()
	cfg, format, err := image.DecodeConfig(f)
	if err != nil {
		return "", 0, 0, err
	}
	return format, cfg.Width, cfg.Height, nil
}

// CreateUser inserts an administrator using a bcrypt hash. Callers must pass
// the password explicitly (the CLI reads TRIBUNA_ADMIN_PASSWORD).
func CreateUser(db *sql.DB, email, password string) error {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" || password == "" {
		return errors.New("email and password are required")
	}
	if len(password) < 12 {
		return errors.New("password must be at least 12 characters")
	}
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	_, err = db.Exec(`INSERT INTO admin_users(email,password_hash,role,is_active,created_at) VALUES(?,?,?,1,?)`, email, string(h), "manager", time.Now().UTC().Format(time.RFC3339))
	return err
}
