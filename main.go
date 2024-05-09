// A simple podcast server.
//
// It creates and serves a podcast feed based on a folder given on the command
// line. It supports mp3/m4a/mp4 files.
//
// References
// [1] https://www.rssboard.org/rss-specification
// [2] https://podcasters.apple.com/support/823-podcast-requirements
// [3] https://help.apple.com/itc/podcasts_connect/#/itcb54353390

package main // import "podserve"

import (
	"bytes"
	"context"
	_ "embed"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"
)

//go:embed cover.png
var cover []byte

const (
	FeedPath  = "/feed"
	CoverPath = "/cover.png"
)

type Server struct {
	mu       sync.RWMutex // Guards FeedXML and Files
	Metadata Metadata
	FeedXML  []byte
	Files    map[string]FileInfo // Path -> File, if it exists.
}

func init() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)
}

// Different tags used to group log messages.
const (
	TagService = "service"
	TagHttp    = "http"
	TagStart   = "start"
	TagRefresh = "refresh"
)

func main() {
	if err := run(); err != nil {
		slog.Error("main", err, "tag", TagService)
		os.Exit(1)
	}
}

func run() error {
	var cfg struct {
		port        int
		dir         string
		externalUrl string
		title       string
		desc        string
		language    string
	}
	flag.IntVar(&cfg.port, "port", 8080, "port on which to serve content")
	flag.StringVar(&cfg.dir, "dir", ".", "directory with media files to serve")
	flag.StringVar(
		&cfg.externalUrl,
		"externalUrl",
		"",
		"external URL to prefix all podcast entries with, "+
			"have to include protocol (http/https) and "+
			"should preferably be an externally reachable url",
	)
	flag.StringVar(&cfg.title, "title", "My Podcast", "podcast title")
	flag.StringVar(&cfg.desc, "desc", "Whatever", "podcast description")
	flag.StringVar(
		&cfg.language,
		"lang", "en", "ISO-639 language code of the show's spoken language",
	)
	flag.Parse()

	if cfg.externalUrl == "" {
		addrs := GetIpAddrs()
		cfg.externalUrl = fmt.Sprintf("http://%s:%d/", addrs[0], cfg.port)
		slog.Warn(
			fmt.Sprintf("-externalUrl left unspecified, using %s", cfg.externalUrl),
			"tag", TagStart,
			"url", cfg.externalUrl,
		)
	}

	if cfg.externalUrl[len(cfg.externalUrl)-1] != '/' {
		cfg.externalUrl += "/"
	}

	srv, err := NewServer(Metadata{
		Title:    cfg.title,
		Link:     cfg.externalUrl + "feed",
		Desc:     cfg.desc,
		Language: "en",
		CoverUrl: cfg.externalUrl + CoverPath[1:],

		externalUrl: cfg.externalUrl,
		localRoot:   cfg.dir,
	})
	if err != nil {
		return err
	}

	mux := http.NewServeMux()
	mux.Handle("/", responseLogger(srv))
	mux.Handle(FeedPath, responseLoggerFunc(srv.ServeFeed))
	mux.Handle(CoverPath, responseLoggerFunc(srv.ServeCover))
	s := &http.Server{
		Addr:           fmt.Sprintf(":%d", cfg.port),
		Handler:        mux,
		ReadTimeout:    120 * time.Second,
		IdleTimeout:    120 * time.Second,
		WriteTimeout:   120 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}

	// Enable graceful shutdown.
	var wg sync.WaitGroup
	sig := make(chan os.Signal, 2)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	ctx, cancel := context.WithCancel(context.Background())

	wg.Add(1)
	go func() {
		defer wg.Done()
		<-sig
		slog.Info("Received signal to shutdown.")
		cancel()
	}()

	wg.Add(1)
	go refreshEntries(ctx, &wg, srv)

	wg.Add(1)
	go func() {
		defer wg.Done()
		<-ctx.Done()
		slog.Info("Shutting down http server", "tag", TagService)
		tctx, tcancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer tcancel()
		if err := s.Shutdown(tctx); err != nil {
			slog.Error("Error shutting down http server.", err, "tag", TagService)
		}
	}()

	fullUrl := cfg.externalUrl + FeedPath[1:]
	initMsg := fmt.Sprintf(
		"Finished initialization, serving %d files. Add %s to your podcast app. Listening on port %d.",
		len(srv.Files), fullUrl, cfg.port,
	)
	slog.Info(initMsg, "tag", TagStart, "num_files", len(srv.Files), "url", fullUrl, "port", cfg.port)
	if err := s.ListenAndServe(); err != http.ErrServerClosed {
		return err
	}
	wg.Wait()
	return nil
}

func GetIpAddrs() []string {
	var ips []string
	host, err := os.Hostname()
	if err != nil {
		slog.Error("Could not get hostname", err, "tag", TagService)
	}
	addrs, err := net.LookupIP(host)
	if err != nil {
		slog.Error("Could not lookup IP", err, "tag", TagService)
	}
	for _, addr := range addrs {
		if ip := addr.To4(); ip != nil {
			ips = append(ips, ip.String())
		}
	}
	if len(ips) == 0 {
		slog.Warn("Did not find an IP address on any interface.", "tag", TagService)
		ips = append(ips, "127.0.0.1")
	}
	return ips
}

func NewServer(m Metadata) (*Server, error) {
	feedXml, files, err := m.GenerateFeed()
	if err != nil {
		return nil, err
	}
	srv := Server{
		mu:       sync.RWMutex{},
		Metadata: m,
		FeedXML:  feedXml,
		Files:    files,
	}
	return &srv, nil
}

func refreshEntries(ctx context.Context, wg *sync.WaitGroup, s *Server) {
	defer wg.Done()
	for {
		select {
		case <-time.After(60 * time.Second):
		case <-ctx.Done():
			return
		}

		feedXml, files, err := s.Metadata.GenerateFeed()
		if err != nil {
			slog.Error("refreshEntries: could not generate podcast items", err, "tag", TagRefresh)
			continue
		}

		if bytes.Equal(feedXml, s.FeedXML) {
			continue
		}

		s.mu.Lock()
		s.FeedXML = feedXml
		s.Files = files
		slog.Info(
			fmt.Sprintf("Updated podcast, now serving %d files.", len(s.Files)),
			"tag", TagRefresh,
			"num_files", len(s.Files),
		)
		s.mu.Unlock()
	}
}

func responseLoggerFunc(f func(w http.ResponseWriter, r *http.Request)) http.Handler {
	return responseLogger(http.HandlerFunc(f))
}

func responseLogger(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w = NewResponseWriter(w)
		defer LogResponse(w.(*ResponseWriter), r)
		h.ServeHTTP(w, r)
	})
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !(r.Method == http.MethodGet || r.Method == http.MethodHead) {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Drop leading slash to map the root against the base dir on the file
	// system.
	requestedFile := r.URL.Path[1:]
	pf, ok := s.Files[requestedFile]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	fp, err := os.Open(pf.Path)
	if err != nil {
		slog.Error("could not open file", err, "file", requestedFile, "tag", TagHttp)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	defer fp.Close()
	w.Header().Add("Content-Type", pf.MimeType)

	// Use http.ServeContent rather than io.Copy to handle range requests.
	// Get less control of the error handling than if we would manage it
	// ourselves though. On the other hand we have a file handle at this point,
	// it should work mostly alright.
	http.ServeContent(w, r, "", pf.ModTime, fp)
}

func (s *Server) ServeFeed(w http.ResponseWriter, r *http.Request) {
	if !(r.Method == http.MethodGet || r.Method == http.MethodHead) {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	w.Header().Add("Content-Type", "application/rss+xml; charset=UTF-8")
	w.Header().Add("Content-Length", strconv.Itoa(len(s.FeedXML)))
	w.WriteHeader(http.StatusOK)
	w.Write(s.FeedXML)
}

func (s *Server) ServeCover(w http.ResponseWriter, r *http.Request) {
	if !(r.Method == http.MethodGet || r.Method == http.MethodHead) {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	w.Header().Add("Content-Type", "image/png")
	w.Header().Add("Content-Length", strconv.Itoa(len(cover)))
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(cover))
}
