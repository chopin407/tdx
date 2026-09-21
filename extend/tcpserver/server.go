package tcpserver

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/injoyai/tdx/extend/httpserver"
)

// Option configures a TCP server.
type Option func(*config)

type config struct {
	addr           string
	token          string
	maxFrame       int
	maxConnections int
	maxInflight    int
	idleTimeout    time.Duration
	requestTimeout time.Duration
	writeTimeout   time.Duration
}

// WithAddr sets the listen address. The default is :8090.
func WithAddr(addr string) Option { return func(c *config) { c.addr = addr } }

// WithToken requires every request to carry this token. Empty disables auth.
func WithToken(token string) Option { return func(c *config) { c.token = token } }

// WithMaxFrame sets the maximum request and response frame size.
func WithMaxFrame(n int) Option { return func(c *config) { c.maxFrame = n } }

// WithMaxConnections sets the simultaneous client limit.
func WithMaxConnections(n int) Option { return func(c *config) { c.maxConnections = n } }

// WithMaxInflight sets the maximum concurrent requests per connection.
func WithMaxInflight(n int) Option { return func(c *config) { c.maxInflight = n } }

// WithIdleTimeout sets how long the server waits for the next frame.
func WithIdleTimeout(d time.Duration) Option { return func(c *config) { c.idleTimeout = d } }

// WithRequestTimeout bounds one routed request.
func WithRequestTimeout(d time.Duration) Option { return func(c *config) { c.requestTimeout = d } }

// WithWriteTimeout bounds one response write.
func WithWriteTimeout(d time.Duration) Option { return func(c *config) { c.writeTimeout = d } }

// Server serves an existing HTTP handler through persistent TCP connections.
// This keeps TCP and HTTP functionality exactly aligned.
type Server struct {
	handler http.Handler
	cfg     config

	mu        sync.Mutex
	listener  net.Listener
	conns     map[net.Conn]struct{}
	closing   chan struct{}
	closeOnce sync.Once
	wg        sync.WaitGroup
}

// New creates a TCP server around an existing handler.
func New(handler http.Handler, opts ...Option) *Server {
	cfg := config{
		addr:           ":8090",
		maxFrame:       DefaultMaxFrame,
		maxConnections: 256,
		maxInflight:    16,
		idleTimeout:    2 * time.Minute,
		requestTimeout: 30 * time.Second,
		writeTimeout:   30 * time.Second,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	if cfg.maxFrame <= 0 {
		cfg.maxFrame = DefaultMaxFrame
	}
	if cfg.maxConnections <= 0 {
		cfg.maxConnections = 1
	}
	if cfg.maxInflight <= 0 {
		cfg.maxInflight = 1
	}
	return &Server{handler: handler, cfg: cfg, conns: make(map[net.Conn]struct{}), closing: make(chan struct{})}
}

// Default creates the full TDX HTTP backend and exposes every route over TCP.
func Default(tcpOpts []Option, httpOpts ...httpserver.Option) (*Server, error) {
	backend, err := httpserver.Default(httpOpts...)
	if err != nil {
		return nil, err
	}
	return New(backend.Handler(), tcpOpts...), nil
}

// Run listens on the configured address and blocks until Close is called.
func (s *Server) Run() error {
	ln, err := net.Listen("tcp", s.cfg.addr)
	if err != nil {
		return err
	}
	return s.Serve(ln)
}

// Serve accepts connections from ln.
func (s *Server) Serve(ln net.Listener) error {
	if s.handler == nil {
		return errors.New("tcpserver: handler is nil")
	}
	s.mu.Lock()
	if s.listener != nil {
		s.mu.Unlock()
		return errors.New("tcpserver: already serving")
	}
	s.listener = ln
	s.mu.Unlock()
	sem := make(chan struct{}, s.cfg.maxConnections)
	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-s.closing:
				return nil
			default:
				return err
			}
		}
		select {
		case sem <- struct{}{}:
			s.track(conn, true)
			s.wg.Add(1)
			go func() {
				defer s.wg.Done()
				defer func() { <-sem; s.track(conn, false); _ = conn.Close() }()
				s.serveConn(conn)
			}()
		default:
			_ = writeFrame(conn, Response{Status: http.StatusServiceUnavailable, Code: 1, Msg: "连接数已达上限"}, s.cfg.maxFrame)
			_ = conn.Close()
		}
	}
}

func (s *Server) track(conn net.Conn, add bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if add {
		s.conns[conn] = struct{}{}
	} else {
		delete(s.conns, conn)
	}
}

func (s *Server) serveConn(conn net.Conn) {
	var writeMu sync.Mutex
	var requests sync.WaitGroup
	sem := make(chan struct{}, s.cfg.maxInflight)
	for {
		if s.cfg.idleTimeout > 0 {
			_ = conn.SetReadDeadline(time.Now().Add(s.cfg.idleTimeout))
		}
		frame, err := readFrame(conn, s.cfg.maxFrame)
		if err != nil {
			break
		}
		var req Request
		if err := json.Unmarshal(frame, &req); err != nil {
			s.writeResponse(conn, &writeMu, Response{Status: http.StatusBadRequest, Code: 1, Msg: "请求 JSON 无效: " + err.Error()})
			continue
		}
		select {
		case sem <- struct{}{}:
			requests.Add(1)
			go func() {
				defer requests.Done()
				defer func() { <-sem }()
				s.writeResponse(conn, &writeMu, s.dispatch(req))
			}()
		case <-s.closing:
			requests.Wait()
			return
		}
	}
	requests.Wait()
}

func (s *Server) writeResponse(conn net.Conn, mu *sync.Mutex, resp Response) {
	mu.Lock()
	defer mu.Unlock()
	if s.cfg.writeTimeout > 0 {
		_ = conn.SetWriteDeadline(time.Now().Add(s.cfg.writeTimeout))
	}
	_ = writeFrame(conn, resp, s.cfg.maxFrame)
}

func (s *Server) dispatch(req Request) Response {
	fail := func(status int, msg string) Response { return Response{ID: req.ID, Status: status, Code: 1, Msg: msg} }
	if req.ID == "" {
		return fail(http.StatusBadRequest, "id 不能为空")
	}
	if s.cfg.token != "" && subtle.ConstantTimeCompare([]byte(req.Token), []byte(s.cfg.token)) != 1 {
		return fail(http.StatusUnauthorized, "认证失败")
	}
	route, err := actionPath(req.Action)
	if err != nil {
		return fail(http.StatusBadRequest, err.Error())
	}
	query, err := encodeParams(req.Params)
	if err != nil {
		return fail(http.StatusBadRequest, err.Error())
	}
	ctx := context.Background()
	if s.cfg.requestTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.cfg.requestTimeout)
		defer cancel()
	}
	r := httptest.NewRequestWithContext(ctx, http.MethodGet, route+"?"+query.Encode(), nil)
	w := httptest.NewRecorder()
	s.handler.ServeHTTP(w, r)
	var envelope struct {
		Code int             `json:"code"`
		Msg  string          `json:"msg"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		return fail(http.StatusBadGateway, "后端响应无效: "+err.Error())
	}
	return Response{ID: req.ID, Status: w.Code, Code: envelope.Code, Msg: envelope.Msg, Data: envelope.Data}
}

func actionPath(action string) (string, error) {
	action = strings.TrimSpace(action)
	if action == "ping" || action == "health" {
		return "/", nil
	}
	action = strings.TrimPrefix(action, "/")
	clean := path.Clean("/" + action)
	if action == "" || clean == "/" || strings.Contains(action, "..") {
		return "", errors.New("action 无效")
	}
	return clean, nil
}

func encodeParams(params map[string]any) (url.Values, error) {
	q := make(url.Values, len(params))
	for k, value := range params {
		if strings.TrimSpace(k) == "" {
			return nil, errors.New("参数名不能为空")
		}
		switch v := value.(type) {
		case string:
			q.Set(k, v)
		case float64:
			q.Set(k, strconv.FormatFloat(v, 'f', -1, 64))
		case bool:
			q.Set(k, strconv.FormatBool(v))
		case []any:
			parts := make([]string, len(v))
			for i, item := range v {
				parts[i] = fmt.Sprint(item)
			}
			q.Set(k, strings.Join(parts, ","))
		case nil:
			q.Set(k, "")
		default:
			q.Set(k, fmt.Sprint(v))
		}
	}
	return q, nil
}

// Addr returns the bound address after Serve/Run starts.
func (s *Server) Addr() net.Addr {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener == nil {
		return nil
	}
	return s.listener.Addr()
}

// Close stops accepting connections, closes active clients and waits for work.
func (s *Server) Close() error {
	var err error
	s.closeOnce.Do(func() {
		close(s.closing)
		s.mu.Lock()
		if s.listener != nil {
			err = s.listener.Close()
		}
		for conn := range s.conns {
			_ = conn.Close()
		}
		s.mu.Unlock()
		s.wg.Wait()
	})
	return err
}
