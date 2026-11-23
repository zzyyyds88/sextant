package engine

import (
	"database/sql"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"chopin-core/internal/httpx"
)

// SMS read access uses the telephony provider's SQLite database opened directly.
//
// The framework route is closed: from a root app_process context,
// `context.getContentResolver()` cannot resolve the TelephonyProvider, because
// ActivityManager refuses to look up a content provider for a caller it cannot
// map to a package ("Unable to find app for caller ... when getting content
// provider sms"). The database itself is readable as root, so this reads it
// read-only and returns the same shape the provider would.
var smsDBPaths = []string{
	"/data/data/com.android.providers.telephony/databases/mmssms.db",
	"/data/user/0/com.android.providers.telephony/databases/mmssms.db",
	"/data/user_de/0/com.android.providers.telephony/databases/mmssms.db",
}

// smsMessage is one row of the sms table.
type smsMessage struct {
	ID      int64  `json:"id"`
	Address string `json:"address"`
	Body    string `json:"body"`
	Date    int64  `json:"date"`
	Type    int    `json:"type"`
	Read    bool   `json:"read"`
}

// handleSMSListDB reads recent SMS from the telephony database.
func (s *Server) handleSMSListDB(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := parseInt(v); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}
	msgs, err := readSMS(limit)
	if err != nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "SMS read failed", err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"count": len(msgs), "messages": msgs})
}

func readSMS(limit int) ([]smsMessage, error) {
	var lastErr error
	for _, path := range smsDBPaths {
		db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&immutable=1")
		if err != nil {
			lastErr = fmt.Errorf("open %s: %w", path, err)
			continue
		}
		rows, err := db.Query(
			`SELECT _id, address, body, date, type, read FROM sms ORDER BY date DESC LIMIT ?`, limit)
		if err != nil {
			_ = db.Close()
			lastErr = fmt.Errorf("query %s: %w", path, err)
			continue
		}
		var out []smsMessage
		for rows.Next() {
			var m smsMessage
			var read sql.NullInt64
			if err := rows.Scan(&m.ID, &m.Address, &m.Body, &m.Date, &m.Type, &read); err != nil {
				lastErr = fmt.Errorf("scan %s: %w", path, err)
				continue
			}
			m.Read = read.Int64 == 1
			out = append(out, m)
		}
		if err := rows.Err(); err != nil {
			lastErr = fmt.Errorf("rows %s: %w", path, err)
		}
		_ = rows.Close()
		_ = db.Close()
		if out != nil || lastErr == nil {
			sort.Slice(out, func(i, j int) bool { return out[i].Date > out[j].Date })
			return out, lastErr
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no SMS database found")
	}
	return nil, lastErr
}

// handleSMSStatusDB reports delivery state from the same database.
func (s *Server) handleSMSStatusDB(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ctx, cancel := contextWithTimeout(r, 10*time.Second)
	defer cancel()
	_ = ctx
	n, err := parseInt(id)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeBadRequest, "id must be a number", nil)
		return
	}
	msgs, err := readSMSByID(int64(n))
	if err != nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "SMS status failed", err)
		return
	}
	if msgs == nil {
		httpx.WriteError(w, http.StatusNotFound, httpx.CodeNotFound, "no such message", nil)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, msgs)
}

func readSMSByID(id int64) (*smsMessage, error) {
	var lastErr error
	for _, path := range smsDBPaths {
		db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&immutable=1")
		if err != nil {
			lastErr = err
			continue
		}
		row := db.QueryRow(`SELECT _id, address, body, date, type, read FROM sms WHERE _id = ?`, id)
		var m smsMessage
		var read sql.NullInt64
		err = row.Scan(&m.ID, &m.Address, &m.Body, &m.Date, &m.Type, &read)
		_ = db.Close()
		if err != nil {
			lastErr = err
			continue
		}
		m.Read = read.Int64 == 1
		return &m, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no SMS database found")
	}
	return nil, lastErr
}

var _ = strings.TrimSpace
