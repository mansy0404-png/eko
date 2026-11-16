package flow

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS flows (
  id TEXT PRIMARY KEY,
  seq INTEGER NOT NULL,
  started_at INTEGER NOT NULL,
  duration_ms INTEGER NOT NULL DEFAULT -1,
  scheme TEXT NOT NULL DEFAULT '',
  host TEXT NOT NULL DEFAULT '',
  port TEXT NOT NULL DEFAULT '',
  method TEXT NOT NULL DEFAULT '',
  path TEXT NOT NULL DEFAULT '',
  query TEXT NOT NULL DEFAULT '',
  url TEXT NOT NULL DEFAULT '',
  proto TEXT NOT NULL DEFAULT '',
  status INTEGER NOT NULL DEFAULT 0,
  req_headers TEXT NOT NULL DEFAULT '[]',
  req_body BLOB,
  req_blob TEXT NOT NULL DEFAULT '',
  req_len INTEGER NOT NULL DEFAULT 0,
  req_trunc INTEGER NOT NULL DEFAULT 0,
  resp_headers TEXT NOT NULL DEFAULT '[]',
  resp_body BLOB,
  resp_blob TEXT NOT NULL DEFAULT '',
  resp_len INTEGER NOT NULL DEFAULT 0,
  resp_trunc INTEGER NOT NULL DEFAULT 0,
  err TEXT NOT NULL DEFAULT '',
  tags TEXT NOT NULL DEFAULT '[]',
  comment TEXT NOT NULL DEFAULT '',
  repeater INTEGER NOT NULL DEFAULT 0,
  intercepted INTEGER NOT NULL DEFAULT 0,
  client TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_flows_seq ON flows(seq DESC);
CREATE INDEX IF NOT EXISTS idx_flows_host ON flows(host);
CREATE INDEX IF NOT EXISTS idx_flows_method ON flows(method);
`

type Store struct {
	db       *sql.DB
	blobDir  string
	inline   int64
	mu       sync.Mutex
	n        int
	maxFlows int
	seq      int64
	dbPath   string
}

func Open(dataDir string, maxFlows int, inline int64) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dataDir, "blobs"), 0o755); err != nil {
		return nil, err
	}
	dbPath := filepath.Join(dataDir, "eko.db")
	dsn := "file:" + dbPath + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	s := &Store{
		db:       db,
		blobDir:  filepath.Join(dataDir, "blobs"),
		inline:   inline,
		maxFlows: maxFlows,
		dbPath:   dbPath,
	}
	if err := db.QueryRow(`SELECT COALESCE(MAX(seq),0), COUNT(*) FROM flows`).Scan(&s.seq, &s.n); err != nil {
		db.Close()
		return nil, err
	}
	s.prune()
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) SetMaxFlows(n int) {
	s.mu.Lock()
	s.maxFlows = n
	s.mu.Unlock()
	s.prune()
}

func (s *Store) SetInlineLimit(n int64) {
	s.mu.Lock()
	s.inline = n
	s.mu.Unlock()
}

func marshalHeaders(h Headers) string {
	if h == nil {
		return "[]"
	}
	b, err := json.Marshal(h)
	if err != nil {
		return "[]"
	}
	return string(b)
}

func unmarshalHeaders(s string) Headers {
	var h Headers
	if s == "" {
		return nil
	}
	if err := json.Unmarshal([]byte(s), &h); err != nil {
		return nil
	}
	return h
}

// body decides whether a body lives in the row or on disk.
func (s *Store) body(id, kind string, data []byte) (inline []byte, blob string, err error) {
	if len(data) == 0 {
		return nil, "", nil
	}
	s.mu.Lock()
	limit := s.inline
	s.mu.Unlock()
	if int64(len(data)) <= limit {
		return data, "", nil
	}
	name := id + "-" + kind + ".bin"
	if err := os.WriteFile(filepath.Join(s.blobDir, name), data, 0o644); err != nil {
		return nil, "", err
	}
	return nil, name, nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (s *Store) Insert(f *Flow) error {
	s.mu.Lock()
	s.seq++
	f.Seq = s.seq
	maxFlows := s.maxFlows
	s.mu.Unlock()

	reqInline, reqBlob, err := s.body(f.ID, "req", f.ReqBody)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT OR REPLACE INTO flows
		(id,seq,started_at,duration_ms,scheme,host,port,method,path,query,url,proto,status,
		 req_headers,req_body,req_blob,req_len,req_trunc,
		 resp_headers,resp_body,resp_blob,resp_len,resp_trunc,
		 err,tags,comment,repeater,intercepted,client)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		f.ID, f.Seq, f.StartedAt.UnixMilli(), f.DurationMs,
		f.Scheme, f.Host, f.Port, f.Method, f.Path, f.Query, f.URL, f.Proto, f.Status,
		marshalHeaders(f.Req.Headers), reqInline, reqBlob, f.Req.BodyLen, boolInt(f.Req.Truncated),
		"[]", nil, "", int64(0), 0,
		f.Err, marshalTags(f.Tags), f.Comment, boolInt(f.Repeater), boolInt(f.Intercepted), f.Client)
	if err != nil {
		return err
	}

	s.mu.Lock()
	s.n++
	n := s.n
	s.mu.Unlock()
	if n > maxFlows {
		s.prune()
	}
	return nil
}

func marshalTags(t []string) string {
	if len(t) == 0 {
		return "[]"
	}
	b, _ := json.Marshal(t)
	return string(b)
}

// Finish persists everything that changes after the response is known.
func (s *Store) Finish(f *Flow) error {
	var respInline []byte
	var respBlob string
	var err error
	if f.RespBody != nil {
		respInline, respBlob, err = s.body(f.ID, "resp", f.RespBody)
		if err != nil {
			return err
		}
	}
	respHeaders := "[]"
	var respLen int64
	var respTrunc bool
	if f.Resp != nil {
		respHeaders = marshalHeaders(f.Resp.Headers)
		respLen = f.Resp.BodyLen
		respTrunc = f.Resp.Truncated
	}
	_, err = s.db.Exec(`UPDATE flows SET
		duration_ms=?, scheme=?, host=?, port=?, method=?, path=?, query=?, url=?, proto=?, status=?,
		req_headers=?, req_len=?, req_trunc=?,
		resp_headers=?, resp_body=?, resp_blob=?, resp_len=?, resp_trunc=?,
		err=?, tags=?, comment=?, intercepted=?
		WHERE id=?`,
		f.DurationMs, f.Scheme, f.Host, f.Port, f.Method, f.Path, f.Query, f.URL, f.Proto, f.Status,
		marshalHeaders(f.Req.Headers), f.Req.BodyLen, boolInt(f.Req.Truncated),
		respHeaders, respInline, respBlob, respLen, boolInt(respTrunc),
		f.Err, marshalTags(f.Tags), f.Comment, boolInt(f.Intercepted), f.ID)
	return err
}

func (s *Store) UpdateComment(id, comment string) error {
	_, err := s.db.Exec(`UPDATE flows SET comment=? WHERE id=?`, comment, id)
	return err
}

type Filter struct {
	Query    string
	Host     string
	Method   string
	Status   string
	Repeater bool
	Limit    int
}

func (s *Store) List(f Filter) ([]*Flow, error) {
	where := []string{}
	args := []any{}
	if f.Query != "" {
		where = append(where, `(url LIKE ? OR host LIKE ? OR method LIKE ?)`)
		q := "%" + f.Query + "%"
		args = append(args, q, q, q)
	}
	if f.Host != "" {
		where = append(where, `host = ?`)
		args = append(args, f.Host)
	}
	if f.Method != "" {
		where = append(where, `method = ?`)
		args = append(args, strings.ToUpper(f.Method))
	}
	if st := f.Status; st != "" {
		if n := len(st); n == 3 && (st[1:] == "xx" || st[1:] == "XX") && st[0] >= '1' && st[0] <= '5' {
			base := int(st[0]-'0') * 100
			where = append(where, `status >= ? AND status <= ?`)
			args = append(args, base, base+99)
		} else if v, err := strconv.Atoi(st); err == nil {
			where = append(where, `status = ?`)
			args = append(args, v)
		}
	}
	if f.Repeater {
		where = append(where, `repeater = 1`)
	}
	limit := f.Limit
	if limit <= 0 || limit > 5000 {
		limit = 1000
	}
	q := `SELECT id,seq,started_at,duration_ms,scheme,host,port,method,path,query,url,proto,status,
		req_len,req_trunc,resp_len,resp_trunc,err,tags,comment,repeater,intercepted,client
		FROM flows`
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY seq DESC LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]*Flow, 0, 64)
	for rows.Next() {
		fl := &Flow{}
		var started int64
		var tags string
		var repeater, intercepted, reqTrunc, respTrunc int
		var respLen int64
		if err := rows.Scan(&fl.ID, &fl.Seq, &started, &fl.DurationMs, &fl.Scheme, &fl.Host, &fl.Port,
			&fl.Method, &fl.Path, &fl.Query, &fl.URL, &fl.Proto, &fl.Status,
			&fl.Req.BodyLen, &reqTrunc, &respLen, &respTrunc, &fl.Err, &tags, &fl.Comment,
			&repeater, &intercepted, &fl.Client); err != nil {
			return nil, err
		}
		fl.StartedAt = time.UnixMilli(started)
		fl.Req.Truncated = reqTrunc == 1
		fl.Repeater = repeater == 1
		fl.Intercepted = intercepted == 1
		if respLen > 0 || fl.Status != 0 {
			fl.Resp = &Message{BodyLen: respLen, Truncated: respTrunc == 1}
		}
		if tags != "" && tags != "[]" {
			_ = json.Unmarshal([]byte(tags), &fl.Tags)
		}
		out = append(out, fl)
	}
	return out, rows.Err()
}

func (s *Store) Get(id string) (*Flow, error) {
	row := s.db.QueryRow(`SELECT id,seq,started_at,duration_ms,scheme,host,port,method,path,query,url,proto,status,
		req_headers,req_body,req_blob,req_len,req_trunc,
		resp_headers,resp_body,resp_blob,resp_len,resp_trunc,
		err,tags,comment,repeater,intercepted,client
		FROM flows WHERE id=?`, id)
	fl := &Flow{}
	var started int64
	var reqHeaders, respHeaders, tags string
	var reqBody, respBody []byte
	var reqBlob, respBlob string
	var reqTrunc, respTrunc, repeater, intercepted int
	var respLen int64
	err := row.Scan(&fl.ID, &fl.Seq, &started, &fl.DurationMs, &fl.Scheme, &fl.Host, &fl.Port,
		&fl.Method, &fl.Path, &fl.Query, &fl.URL, &fl.Proto, &fl.Status,
		&reqHeaders, &reqBody, &reqBlob, &fl.Req.BodyLen, &reqTrunc,
		&respHeaders, &respBody, &respBlob, &respLen, &respTrunc,
		&fl.Err, &tags, &fl.Comment, &repeater, &intercepted, &fl.Client)
	if err != nil {
		return nil, err
	}
	fl.StartedAt = time.UnixMilli(started)
	fl.Req.Headers = unmarshalHeaders(reqHeaders)
	fl.Req.Truncated = reqTrunc == 1
	fl.ReqBody = reqBody
	if reqBlob != "" {
		if b, err := os.ReadFile(filepath.Join(s.blobDir, reqBlob)); err == nil {
			fl.ReqBody = b
		}
	}
	fl.Repeater = repeater == 1
	fl.Intercepted = intercepted == 1
	if respHeaders != "[]" || respLen > 0 || fl.Status > 0 {
		fl.Resp = &Message{Headers: unmarshalHeaders(respHeaders), BodyLen: respLen, Truncated: respTrunc == 1}
		fl.RespBody = respBody
		if respBlob != "" {
			if b, err := os.ReadFile(filepath.Join(s.blobDir, respBlob)); err == nil {
				fl.RespBody = b
			}
		}
	}
	if tags != "" && tags != "[]" {
		_ = json.Unmarshal([]byte(tags), &fl.Tags)
	}
	return fl, nil
}

func (s *Store) Delete(id string) error {
	var reqBlob, respBlob string
	_ = s.db.QueryRow(`SELECT req_blob, resp_blob FROM flows WHERE id=?`, id).Scan(&reqBlob, &respBlob)
	res, err := s.db.Exec(`DELETE FROM flows WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		s.mu.Lock()
		if s.n > 0 {
			s.n--
		}
		s.mu.Unlock()
	}
	s.removeBlobs(reqBlob, respBlob)
	return nil
}

func (s *Store) Clear() error {
	var reqBlob, respBlob string
	rows, err := s.db.Query(`SELECT req_blob, resp_blob FROM flows`)
	if err == nil {
		for rows.Next() {
			if rows.Scan(&reqBlob, &respBlob) == nil {
				s.removeBlobs(reqBlob, respBlob)
			}
		}
		rows.Close()
	}
	if _, err := s.db.Exec(`DELETE FROM flows`); err != nil {
		return err
	}
	s.mu.Lock()
	s.n = 0
	s.mu.Unlock()
	return nil
}

func (s *Store) removeBlobs(names ...string) {
	for _, name := range names {
		if name != "" {
			_ = os.Remove(filepath.Join(s.blobDir, name))
		}
	}
}

func (s *Store) prune() {
	s.mu.Lock()
	maxFlows := s.maxFlows
	n := s.n
	s.mu.Unlock()
	if maxFlows <= 0 || n <= maxFlows {
		return
	}
	remove := n - maxFlows
	rows, err := s.db.Query(`SELECT id, req_blob, resp_blob FROM flows ORDER BY seq ASC LIMIT ?`, remove)
	if err != nil {
		return
	}
	type victim struct {
		id, req, resp string
	}
	var victims []victim
	for rows.Next() {
		var v victim
		if rows.Scan(&v.id, &v.req, &v.resp) == nil {
			victims = append(victims, v)
		}
	}
	rows.Close()
	for _, v := range victims {
		if _, err := s.db.Exec(`DELETE FROM flows WHERE id=?`, v.id); err == nil {
			s.mu.Lock()
			if s.n > 0 {
				s.n--
			}
			s.mu.Unlock()
			s.removeBlobs(v.req, v.resp)
		}
	}
}

type Stats struct {
	Flows    int   `json:"flows"`
	DBSize   int64 `json:"dbSize"`
	BlobSize int64 `json:"blobSize"`
	MaxFlows int   `json:"maxFlows"`
	Seq      int64 `json:"seq"`
}

func (s *Store) Stats() Stats {
	s.mu.Lock()
	st := Stats{Flows: s.n, MaxFlows: s.maxFlows, Seq: s.seq}
	s.mu.Unlock()
	if fi, err := os.Stat(s.dbPath); err == nil {
		st.DBSize = fi.Size()
	}
	if fi, err := os.Stat(s.dbPath + "-wal"); err == nil {
		st.DBSize += fi.Size()
	}
	if entries, err := os.ReadDir(s.blobDir); err == nil {
		for _, e := range entries {
			if fi, err := e.Info(); err == nil {
				st.BlobSize += fi.Size()
			}
		}
	}
	return st
}

func (s *Store) String() string {
	st := s.Stats()
	return fmt.Sprintf("%d flows, %s on disk", st.Flows, humanBytes(st.DBSize+st.BlobSize))
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGT"[exp])
}
