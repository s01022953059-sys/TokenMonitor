package main

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
	_ "modernc.org/sqlite"
)

const renameCooldown = 7 * 24 * time.Hour
const oldNameProtection = 30 * 24 * time.Hour

var phonePattern = regexp.MustCompile(`[0-9]{11}`)
var protectedNameSkeletons = []string{
	"official", "admin", "administrator", "root", "system", "support", "staff", "moderator",
	"tokenmonitor", "官方", "管理员", "客服", "系统", "支持", "运营", "版主",
}
var contactMarkers = []string{"http", "www", "微信", "vx", "wx", "qq", "加我", "联系我"}

type profileRequest struct {
	ID           string `json:"id"`
	DeviceSecret string `json:"device_secret"`
	DisplayName  string `json:"display_name"`
}

type profileResult struct {
	DisplayName  string
	Canonical    string
	NextChangeAt time.Time
	NoChange     bool
}

type profileError struct {
	status       int
	code         string
	message      string
	nextChangeAt time.Time
}

func (e *profileError) Error() string { return e.message }

type profileDatabase struct {
	db *sql.DB
}

func openProfileDatabase(path string) (*profileDatabase, error) {
	if err := os.MkdirAll(parentDir(path), 0750); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	for _, statement := range []string{
		`PRAGMA journal_mode=WAL`,
		`PRAGMA busy_timeout=5000`,
		`CREATE TABLE IF NOT EXISTS profiles (
			user_id TEXT PRIMARY KEY,
			display_name TEXT NOT NULL,
			canonical_name TEXT NOT NULL UNIQUE,
			created_at TEXT NOT NULL,
			changed_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS name_reservations (
			canonical_name TEXT PRIMARY KEY,
			owner_id TEXT NOT NULL,
			reserved_until TEXT NOT NULL
		)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			db.Close()
			return nil, err
		}
	}
	return &profileDatabase{db: db}, nil
}

func parentDir(path string) string {
	index := strings.LastIndexAny(path, `/\`)
	if index <= 0 {
		return "."
	}
	return path[:index]
}

func (p *profileDatabase) Close() error { return p.db.Close() }

func normalizeDisplayName(input string, blockedNamesPath string) (string, string, error) {
	display := strings.TrimSpace(norm.NFKC.String(input))
	if !utf8.ValidString(display) {
		return "", "", errors.New("昵称编码不正确")
	}
	runes := []rune(display)
	if len(runes) < 2 || len(runes) > 16 {
		return "", "", errors.New("昵称需要 2–16 个字符")
	}
	hasLetter := false
	repeat := 1
	for index, current := range runes {
		allowed := current == '_' || current >= '0' && current <= '9' ||
			current >= 'A' && current <= 'Z' || current >= 'a' && current <= 'z' || unicode.Is(unicode.Han, current)
		if !allowed {
			return "", "", errors.New("昵称只能使用中文、英文字母、数字和下划线")
		}
		if current >= 'A' && current <= 'Z' || current >= 'a' && current <= 'z' || unicode.Is(unicode.Han, current) {
			hasLetter = true
		}
		if index > 0 && unicode.ToLower(current) == unicode.ToLower(runes[index-1]) {
			repeat++
			if repeat >= 4 {
				return "", "", errors.New("昵称不能连续重复同一字符")
			}
		} else {
			repeat = 1
		}
	}
	if !hasLetter {
		return "", "", errors.New("昵称至少需要一个中文或英文字母")
	}
	canonical := strings.ToLower(display)
	if strings.HasPrefix(canonical, "user_") {
		return "", "", errors.New("该昵称格式不可使用")
	}
	skeleton := nameSkeleton(canonical)
	for _, protected := range protectedNameSkeletons {
		if strings.Contains(skeleton, nameSkeleton(protected)) {
			return "", "", errors.New("该昵称包含受保护名称")
		}
	}
	for _, marker := range contactMarkers {
		if strings.Contains(canonical, marker) {
			return "", "", errors.New("昵称不能包含网址或联系方式")
		}
	}
	if phonePattern.MatchString(canonical) {
		return "", "", errors.New("昵称不能包含网址或联系方式")
	}
	for _, blocked := range loadBlockedNames(blockedNamesPath) {
		if blocked != "" && strings.Contains(canonical, blocked) {
			return "", "", errors.New("该昵称不可使用")
		}
	}
	return display, canonical, nil
}

func nameSkeleton(value string) string {
	value = strings.ToLower(norm.NFKC.String(value))
	var builder strings.Builder
	for _, current := range value {
		switch current {
		case '_':
			continue
		case '0':
			current = 'o'
		case '1':
			current = 'i'
		}
		builder.WriteRune(current)
	}
	return builder.String()
}

func loadBlockedNames(path string) []string {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var result []string
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(norm.NFKC.String(line))
		if line != "" && !strings.HasPrefix(line, "#") {
			result = append(result, strings.ToLower(line))
		}
	}
	return result
}

func validateDeviceSecret(encoded, expectedHash string) bool {
	secret, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(secret) != 32 || len(expectedHash) != sha256.Size*2 {
		return false
	}
	hash := sha256.Sum256(secret)
	actual := hex.EncodeToString(hash[:])
	return subtle.ConstantTimeCompare([]byte(actual), []byte(expectedHash)) == 1
}

func (p *profileDatabase) updateName(ctx context.Context, userID, displayName, canonicalName string, now time.Time, syncReport func() error) (profileResult, bool, error) {
	connection, err := p.db.Conn(ctx)
	if err != nil {
		return profileResult{}, false, err
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return profileResult{}, false, err
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = connection.ExecContext(context.Background(), "ROLLBACK")
		}
	}()

	_, _ = connection.ExecContext(ctx, "DELETE FROM name_reservations WHERE reserved_until <= ?", now.UTC().Format(time.RFC3339))
	var currentName, currentCanonical, createdAtRaw, changedAtRaw string
	profileExists := true
	err = connection.QueryRowContext(ctx, "SELECT display_name, canonical_name, created_at, changed_at FROM profiles WHERE user_id = ?", userID).Scan(&currentName, &currentCanonical, &createdAtRaw, &changedAtRaw)
	if errors.Is(err, sql.ErrNoRows) {
		profileExists = false
	} else if err != nil {
		return profileResult{}, false, err
	}
	if profileExists && currentCanonical == canonicalName {
		changedAt, _ := time.Parse(time.RFC3339, changedAtRaw)
		_, _ = connection.ExecContext(ctx, "ROLLBACK")
		committed = true
		return profileResult{DisplayName: currentName, Canonical: currentCanonical, NextChangeAt: changedAt.Add(renameCooldown), NoChange: true}, false, nil
	}
	if profileExists {
		changedAt, parseErr := time.Parse(time.RFC3339, changedAtRaw)
		if parseErr == nil && now.Before(changedAt.Add(renameCooldown)) {
			return profileResult{}, false, &profileError{
				status: 429, code: "rename_cooldown", message: "昵称每 7 天可以修改一次", nextChangeAt: changedAt.Add(renameCooldown),
			}
		}
	}
	var owner string
	err = connection.QueryRowContext(ctx, "SELECT user_id FROM profiles WHERE canonical_name = ?", canonicalName).Scan(&owner)
	if err == nil && owner != userID {
		return profileResult{}, false, &profileError{status: 409, code: "name_taken", message: "昵称已被使用"}
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return profileResult{}, false, err
	}
	var reservationOwner string
	err = connection.QueryRowContext(ctx, "SELECT owner_id FROM name_reservations WHERE canonical_name = ?", canonicalName).Scan(&reservationOwner)
	if err == nil && reservationOwner != userID {
		return profileResult{}, false, &profileError{status: 409, code: "name_reserved", message: "该昵称暂时处于保护期"}
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return profileResult{}, false, err
	}

	nowRaw := now.UTC().Format(time.RFC3339)
	createdAt := nowRaw
	if profileExists {
		createdAt = createdAtRaw
		if _, err := connection.ExecContext(ctx, "INSERT OR REPLACE INTO name_reservations(canonical_name, owner_id, reserved_until) VALUES(?, ?, ?)", currentCanonical, userID, now.Add(oldNameProtection).UTC().Format(time.RFC3339)); err != nil {
			return profileResult{}, false, err
		}
	}
	if _, err := connection.ExecContext(ctx, `INSERT INTO profiles(user_id, display_name, canonical_name, created_at, changed_at)
		VALUES(?, ?, ?, ?, ?) ON CONFLICT(user_id) DO UPDATE SET display_name=excluded.display_name,
		canonical_name=excluded.canonical_name, changed_at=excluded.changed_at`, userID, displayName, canonicalName, createdAt, nowRaw); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return profileResult{}, false, &profileError{status: 409, code: "name_taken", message: "昵称已被使用"}
		}
		return profileResult{}, false, err
	}
	if err := syncReport(); err != nil {
		return profileResult{}, false, &profileError{status: 502, code: "upload_failed", message: "昵称同步失败，请稍后重试"}
	}
	if _, err := connection.ExecContext(ctx, "COMMIT"); err != nil {
		return profileResult{}, true, err
	}
	committed = true
	return profileResult{DisplayName: displayName, Canonical: canonicalName, NextChangeAt: now.Add(renameCooldown)}, true, nil
}

func profileDatabasePath() string {
	if value := strings.TrimSpace(os.Getenv("PROFILE_DB_PATH")); value != "" {
		return value
	}
	return "/var/lib/token-monitor-community/community.db"
}

func blockedNamesPath() string {
	if value := strings.TrimSpace(os.Getenv("BLOCKED_NAMES_FILE")); value != "" {
		return value
	}
	return "/etc/token-monitor-community-blocked-names.txt"
}

func profileStorageError(err error) *profileError {
	var typed *profileError
	if errors.As(err, &typed) {
		return typed
	}
	return &profileError{status: 503, code: "storage_unavailable", message: "昵称服务暂时不可用"}
}

func validateProfileRequest(request profileRequest) error {
	if !communityIDPattern.MatchString(request.ID) {
		return fmt.Errorf("匿名 ID 格式不正确")
	}
	if len(request.DeviceSecret) > 128 || len(request.DisplayName) > 128 {
		return fmt.Errorf("请求内容过长")
	}
	return nil
}
