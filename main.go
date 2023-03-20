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
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"golang.org/x/exp/slog"
)

//go:embed cover.png
var cover []byte

const (
	FeedPath  = "/feed"
	CoverPath = "/cover.png"
)

type Server struct {
	mu      sync.RWMutex // Guards FeedXML and Files
	Meta    Meta
	FeedXML []byte
	Files   map[string]File // Path -> File, if it exists.
}

func init() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout))
	slog.SetDefault(logger)
}

func main() {
	if err := run(); err != nil {
		slog.Error("main", err)
		os.Exit(1)
	}
}

func run() error {
	var port int
	var dir, externalUrl, title, desc, language string
	flag.IntVar(&port, "port", 8080, "port on which to serve content")
	flag.StringVar(&dir, "dir", ".", "directory with media files to serve")
	flag.StringVar(
		&externalUrl,
		"externalUrl",
		"",
		"external URL to prefix all podcast entries with, "+
			"have to include protocol (http/https) and "+
			"should preferably be an externally reachable url",
	)
	flag.StringVar(&title, "title", "My Podcast", "podcast title")
	flag.StringVar(&desc, "desc", "Whatever", "podcast description")
	flag.StringVar(
		&language,
		"lang", "en", "ISO-639 language code of the show's spoken language",
	)
	flag.Parse()

	if externalUrl == "" {
		addrs := GetIpAddrs()
		externalUrl = fmt.Sprintf("http://%s:%d/", addrs[0], port)
		slog.Warn(fmt.Sprintf("-externalUrl left unspecified, using %s", externalUrl), "url", externalUrl)
	}

	if externalUrl[len(externalUrl)-1] != '/' {
		externalUrl += "/"
	}

	srv, err := NewServer(Meta{
		Title:    title,
		Link:     externalUrl + "feed",
		Desc:     desc,
		Language: "en",
		CoverUrl: externalUrl + CoverPath[1:],

		externalUrl: externalUrl,
		localRoot:   dir,
	})
	if err != nil {
		return err
	}

	mux := http.NewServeMux()
	mux.Handle("/", srv)
	mux.HandleFunc(FeedPath, srv.ServeFeed)
	mux.HandleFunc(CoverPath, srv.ServeCover)
	s := &http.Server{
		Addr:           fmt.Sprintf(":%d", port),
		Handler:        mux,
		ReadTimeout:    120 * time.Second,
		IdleTimeout:    120 * time.Second,
		WriteTimeout:   120 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go refreshEntries(ctx, done, srv)

	// Enable graceful shutdown.
	shutdown := make(chan struct{})
	sig := make(chan os.Signal, 2)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sig
		cancel()
		if err := s.Shutdown(context.Background()); err != nil {
			slog.Error("error shutting down http server", err)
		}
		<-done
		close(shutdown)
	}()

	fullUrl := externalUrl + FeedPath[1:]
	slog.Info(fmt.Sprintf("Finished initialization, serving %d files.", len(srv.Files)), "num_files", len(srv.Files))
	slog.Info(fmt.Sprintf("Add %s to your podcast app.", fullUrl), "url", fullUrl)
	slog.Info(fmt.Sprintf("Listening on port %d", port), "port", port)
	if err := s.ListenAndServe(); err != http.ErrServerClosed {
		return err
	}
	<-shutdown
	return nil
}

func GetIpAddrs() []string {
	var ips []string
	host, err := os.Hostname()
	if err != nil {
		slog.Error("Could not get hostname", err)
	}
	addrs, err := net.LookupIP(host)
	if err != nil {
		slog.Error("Could not lookup IP", err)
	}
	for _, addr := range addrs {
		if ip := addr.To4(); ip != nil {
			ips = append(ips, ip.String())
		}
	}
	if len(ips) == 0 {
		slog.Warn("Did not find an IP address on any interface.")
		ips = append(ips, "127.0.0.1")
	}
	return ips
}

func NewServer(m Meta) (*Server, error) {
	feedXml, files, err := m.GenerateFeed()
	if err != nil {
		return nil, err
	}
	srv := Server{
		mu:      sync.RWMutex{},
		Meta:    m,
		FeedXML: feedXml,
		Files:   files,
	}
	return &srv, nil
}

func refreshEntries(ctx context.Context, done chan<- struct{}, s *Server) {
	for {
		select {
		case <-time.After(60 * time.Second):
		case <-ctx.Done():
			done <- struct{}{}
			return
		}

		feedXml, files, err := s.Meta.GenerateFeed()
		if err != nil {
			slog.Error("refreshEntries: could not generate podcast items", err)
			continue
		}

		if bytes.Equal(feedXml, s.FeedXML) {
			continue
		}

		s.mu.Lock()
		s.FeedXML = feedXml
		s.Files = files
		slog.Info(fmt.Sprintf("Updated podcast, now serving %d files.", len(s.Files)), "num_files", len(s.Files))
		s.mu.Unlock()
	}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w = NewResponseWriter(w)
	defer LogResponse(w.(*ResponseWriter), r)
	if !(r.Method == http.MethodGet || r.Method == http.MethodHead) {
		w.WriteHeader(http.StatusBadRequest)
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
		slog.Error("could not open file", err, "file", requestedFile)
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
	w = NewResponseWriter(w)
	defer LogResponse(w.(*ResponseWriter), r)
	if !(r.Method == http.MethodGet || r.Method == http.MethodHead) {
		w.WriteHeader(http.StatusBadRequest)
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
	w = NewResponseWriter(w)
	defer LogResponse(w.(*ResponseWriter), r)
	if !(r.Method == http.MethodGet || r.Method == http.MethodHead) {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	w.Header().Add("Content-Type", "image/png")
	w.Header().Add("Content-Length", strconv.Itoa(len(cover)))
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(cover))
}
