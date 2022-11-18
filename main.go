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
	"html/template"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"golang.org/x/exp/slog"
)

//go:embed cover.png
var cover []byte

const (
	XMLHeader = `<?xml version="1.0" encoding="UTF-8"?>`
	// See the references in the package comment for a description of supported
	// fields.
	RSSTemplate = `
<rss version="2.0"
 xmlns:itunes="http://www.itunes.com/dtds/podcast-1.0.dtd"
 xmlns:content="http://purl.org/rss/1.0/modules/content/"
>
<channel>
 <title>{{.Meta.Title}}</title>
 <link>{{.Meta.Link}}</link>
 <description>{{.Meta.Desc}}</description>
 <language>{{.Meta.Language}}</language>
 <itunes:image href="{{.Meta.CoverUrl}}" />
 {{range .Items}}
 <item>
  <title>{{.Title}}</title>
  <link>{{.Link}}</link>
  <description>{{.Desc}}</description>
  <enclosure url="{{.Enclosure.Url}}" length="{{.Enclosure.Length}}" Type="{{.Enclosure.Type}}" />
 </item>
 {{- end}}
</channel>
</rss>
`
)

type PodcastTemplateData struct {
	Meta  PodcastDesc
	Items []PodcastItem
}

type PodcastDesc struct {
	Title    string
	Link     string
	Desc     string
	Language string
	CoverUrl string

	externalUrl    string
	feedServePath  string
	coverServePath string
	localRoot      string
}

type PodcastItem struct {
	Title     string
	Path      string
	ModTime   time.Time
	Link      string
	Desc      string
	Enclosure Enclosure
}

type Enclosure struct {
	Url    string
	Length int64
	Type   string
}

type PodcastServer struct {
	mu      sync.RWMutex // Guards FeedXML and Files
	Desc    PodcastDesc
	FeedXML []byte
	Files   map[string]PodcastFile // Path -> PodcastFile, if it exists.
}

type PodcastFile struct {
	Path     string
	MimeType string
	Size     int64
	ModTime  time.Time
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

	podDesc := PodcastDesc{
		Title:    title,
		Link:     externalUrl + "feed",
		Desc:     desc,
		Language: "en",
		CoverUrl: externalUrl + "cover.png",

		externalUrl:    externalUrl,
		coverServePath: "/cover.png",
		feedServePath:  "/feed",
		localRoot:      dir,
	}

	srv, err := NewPodcastServer(podDesc)
	if err != nil {
		return err
	}

	mux := http.NewServeMux()
	mux.Handle("/", srv)
	mux.HandleFunc(srv.Desc.feedServePath, srv.ServeFeed)
	mux.HandleFunc(srv.Desc.coverServePath, srv.ServeCover)
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
	go refreshPodcastEntries(ctx, done, srv)

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

	fullUrl := externalUrl + srv.Desc.feedServePath[1:]
	slog.Info(fmt.Sprintf("Finished initialization, serving %d files.", len(srv.Files)), "num_files", len(srv.Files))
	slog.Info(fmt.Sprintf("Add %s to your podcast app.", fullUrl), "url", fullUrl)
	slog.Info(fmt.Sprintf("Listening on port %d", port), "port", port)
	if err := s.ListenAndServe(); err != http.ErrServerClosed {
		return err
	}
	<-shutdown
	return nil
}

// I only use mp3/mp4 audio and have therefore only mapped those.
//
// Citing Apple [3], the following are supported, at least on iOS:
//
// "The type values for the supported file formats are: audio/x-m4a,
// audio/mpeg, video/quicktime, video/mp4, video/x-m4v, and application/pdf."
var mimeType = map[string]string{
	".mp3": "audio/mpeg",
	".mp4": "audio/x-m4a",
	".m4a": "audio/x-m4a",
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

func NewPodcastServer(desc PodcastDesc) (*PodcastServer, error) {
	feedXml, files, err := desc.GenerateFeed()
	if err != nil {
		return nil, err
	}
	srv := PodcastServer{
		mu:      sync.RWMutex{},
		Desc:    desc,
		FeedXML: feedXml,
		Files:   files,
	}
	return &srv, nil
}

func refreshPodcastEntries(ctx context.Context, done chan<- struct{}, s *PodcastServer) {
	for {
		select {
		case <-time.After(60 * time.Second):
		case <-ctx.Done():
			done <- struct{}{}
			return
		}

		feedXml, files, err := s.Desc.GenerateFeed()
		if err != nil {
			slog.Error("refreshPodcastServer: could not generate podcast items", err)
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

func (desc PodcastDesc) GenerateFeed() (feedXml []byte, files map[string]PodcastFile, err error) {
	podItems, err := desc.podcastItems()
	if err != nil {
		return nil, nil, err
	}
	feedXml, err = desc.feed(podItems)
	if err != nil {
		return nil, nil, err
	}
	files = make(map[string]PodcastFile)
	for _, it := range podItems {
		files[it.Path] = PodcastFile{
			Path:     filepath.Join(desc.localRoot, it.Path),
			MimeType: it.Enclosure.Type,
			Size:     it.Enclosure.Length,
			ModTime:  it.ModTime,
		}
	}
	return feedXml, files, nil
}

// Reads the local file system and returns a slice of available PodcastItems
// with all the metadata required to serve them.
func (desc PodcastDesc) podcastItems() ([]PodcastItem, error) {
	if desc.externalUrl[len(desc.externalUrl)-1] != '/' {
		panic("PodcastItems: expected externalUrl to end in '/'")
	}
	var pp []PodcastItem
	fsys := os.DirFS(desc.localRoot)
	err := fs.WalkDir(fsys, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		ext := filepath.Ext(name)

		if mime, ok := mimeType[ext]; ok {
			f, err := os.Open(filepath.Join(desc.localRoot, path))
			if err != nil {
				return err
			}
			defer f.Close()
			title := name[:len(name)-len(ext)]
			info, err := d.Info()
			if err != nil {
				return err
			}
			url, err := url.Parse(desc.externalUrl + url.PathEscape(path))
			if err != nil {
				return err
			}
			pp = append(pp, PodcastItem{
				Title:   title,
				Path:    path,
				ModTime: info.ModTime(),
				Link:    url.String(),
				Desc:    "",
				Enclosure: Enclosure{
					Url:    url.String(),
					Length: info.Size(),
					Type:   mime,
				},
			})
		}
		return nil
	})
	return pp, err
}

func (desc PodcastDesc) feed(items []PodcastItem) ([]byte, error) {
	tmpl := template.Must(template.New("rss").Parse(RSSTemplate))
	buf := bytes.NewBuffer(nil)
	buf.Write([]byte(XMLHeader))
	err := tmpl.Execute(buf, PodcastTemplateData{desc, items})
	return buf.Bytes(), err
}

func (s *PodcastServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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

func (s *PodcastServer) ServeFeed(w http.ResponseWriter, r *http.Request) {
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

func (s *PodcastServer) ServeCover(w http.ResponseWriter, r *http.Request) {
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
