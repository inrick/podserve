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
	"path"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
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
 <title>{{.Title}}</title>
 <link>{{.Link}}/</link>
 <description>{{.Desc}}</description>
 <language>{{.Language}}</language>
 <itunes:image href="{{.CoverUrl}}" />

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

type Podcast struct {
	Title    string
	Link     string
	Desc     string
	Language string
	CoverUrl string

	Items []PodcastItem
}

type Enclosure struct {
	Url    string
	Length int64
	Type   string
}

type PodcastItem struct {
	Title     string
	Path      string
	ModTime   time.Time
	Link      string
	Desc      string
	Enclosure Enclosure
}

type PodcastFile struct {
	MimeType string
	Size     int64
	ModTime  time.Time
}

type PodcastServer struct {
	RootPath string
	Files    map[string]PodcastFile // Path -> PodcastFile, if it exists.
}

func main() {
	if err := run(); err != nil {
		Error("%v", err)
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
	flag.BoolVar(&DebugLog, "debug", false, "enable debug log")
	flag.Parse()

	if externalUrl == "" {
		addrs := GetIpAddrs()
		externalUrl = fmt.Sprintf("http://%s:%d/", addrs[0], port)
		Warning("-externalUrl left unspecified, using %q", externalUrl)
	}

	if externalUrl[len(externalUrl)-1] != '/' {
		externalUrl += "/"
	}

	podItems, err := GetPodcastItems(externalUrl, dir)
	if err != nil {
		return err
	}
	pod := Podcast{
		Title:    title,
		Link:     externalUrl,
		Desc:     desc,
		Language: "en",
		CoverUrl: externalUrl + "cover.png",
		Items:    podItems,
	}
	tmpl := template.Must(template.New("rss").Parse(RSSTemplate))
	buf := bytes.NewBuffer(nil)
	buf.Write([]byte(XMLHeader))
	if err := tmpl.Execute(buf, pod); err != nil {
		return err
	}

	fs := PodcastServer{
		RootPath: dir,
		Files:    make(map[string]PodcastFile),
	}
	for _, it := range pod.Items {
		fs.Files[it.Path] = PodcastFile{
			MimeType: it.Enclosure.Type,
			Size:     it.Enclosure.Length,
			ModTime:  it.ModTime,
		}
	}

	feedPath := "/feed"
	mux := http.NewServeMux()
	mux.Handle("/", fs)
	mux.HandleFunc(feedPath, func(w http.ResponseWriter, r *http.Request) {
		defer LogResponse(w, r)
		w.Header().Add("Content-Type", "application/rss+xml; charset=UTF-8")
		w.Header().Add("Content-Length", strconv.Itoa(len(buf.Bytes())))
		w.WriteHeader(http.StatusOK)
		w.Write(buf.Bytes())
	})
	mux.HandleFunc("/cover.png", func(w http.ResponseWriter, r *http.Request) {
		defer LogResponse(w, r)
		w.Header().Add("Content-Type", "image/png")
		w.Header().Add("Content-Length", strconv.Itoa(len(cover)))
		http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(cover))
	})
	s := &http.Server{
		Addr:           fmt.Sprintf(":%d", port),
		Handler:        mux,
		ReadTimeout:    120 * time.Second,
		IdleTimeout:    120 * time.Second,
		WriteTimeout:   120 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}

	// Enable graceful shutdown.
	shutdown := make(chan struct{})
	sig := make(chan os.Signal, 2)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sig
		if err := s.Shutdown(context.Background()); err != nil {
			Error("%v", err)
		}
		close(shutdown)
	}()

	Info("Finished initialization, serving %d files.", len(pod.Items))
	Info("Add %s to your podcast app.", externalUrl+feedPath[1:])
	Info("Listening on port %d.", port)
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
		Error("Could not get hostname: %v", err)
	}
	addrs, err := net.LookupIP(host)
	if err != nil {
		Error("Could not lookup IP: %v", err)
	}
	for _, addr := range addrs {
		if ip := addr.To4(); ip != nil {
			ips = append(ips, ip.String())
		}
	}
	if len(ips) == 0 {
		Warning("Did not find an IP address on any interface.")
		ips = append(ips, "127.0.0.1")
	}
	return ips
}

func GetPodcastItems(linkPrefix, dir string) ([]PodcastItem, error) {
	if linkPrefix[len(linkPrefix)-1] != '/' {
		panic("GetPodcastItems: expected linkPrefix to end in '/'")
	}
	var pp []PodcastItem
	fsys := os.DirFS(dir)
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
			f, err := os.Open(filepath.Join(dir, path))
			if err != nil {
				return err
			}
			defer f.Close()
			title := name[:len(name)-len(ext)]
			info, err := d.Info()
			if err != nil {
				return err
			}
			url, err := url.Parse(linkPrefix + url.PathEscape(path))
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

func (s PodcastServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	defer LogResponse(w, r)
	if !(r.Method == http.MethodGet || r.Method == http.MethodHead) {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	// Drop leading slash to map the root against the base dir on the file
	// system.
	requestedFile := r.URL.Path[1:]
	pf, ok := s.Files[requestedFile]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	fp, err := os.Open(path.Join(s.RootPath, requestedFile))
	if err != nil {
		Error("could not open file %q: %v", requestedFile, err)
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
