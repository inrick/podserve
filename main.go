package main // import "podserve"

import (
	"bytes"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"text/template"

	"github.com/bogem/id3v2"
)

func assert(b bool) {
	if !b {
		panic("assert failed")
	}
}

func ck(err error) {
	if err != nil {
		panic(err)
	}
}

// Possibly add optional channel elements, see
// https://www.rssboard.org/rss-specification
// for what is available.
//
// See also
// https://podcasters.apple.com/support/823-podcast-requirements
// https://help.apple.com/itc/podcasts_connect/#/itcb54353390
const RSSTemplate = `
<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:itunes="http://www.itunes.com/dtds/podcast-1.0.dtd" xmlns:content="http://purl.org/rss/1.0/modules/content/">
<channel>
 <title>{{.Title}}</title>
 <link>{{.Link}}/</link>
 <description>{{.Desc}}</description>

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

type Podcast struct {
	Title string
	Link  string
	Desc  string

	Items []PodcastItem
}

type Enclosure struct {
	Url    string
	Length int64
	Type   string
}

type PodcastItem struct {
	Title     string
	Link      string
	Desc      string
	Enclosure Enclosure
}

func main() {
	var port int
	var dir string
	var verbose bool
	var baseUrl string
	flag.IntVar(&port, "port", 8080, "port on which to serve content")
	flag.StringVar(&dir, "dir", ".", "directory to serve")
	flag.BoolVar(&verbose, "verbose", false, "enable verbose logging (dumps incoming request)")
	flag.StringVar(&baseUrl, "baseUrl", "http://localhost:8080/", "base url with which to prefix all podcast entries")
	flag.Parse()

	if baseUrl[len(baseUrl)-1] != '/' {
		baseUrl += "/"
	}

	pod := Podcast{
		Title: "My Podcast",
		Link:  baseUrl,
		Desc:  "Whatever",
		Items: GetPodcastItems(baseUrl, dir),
	}
	tmpl := template.Must(template.New("rss").Parse(RSSTemplate))
	buf := bytes.NewBuffer(nil)
	ck(tmpl.Execute(buf, pod))

	fs := http.FileServer(http.Dir(dir))
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		logRequest(r, verbose)
		fs.ServeHTTP(w, r)
	})
	http.HandleFunc("/pod", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write(buf.Bytes())
	})

	log.Printf("Finished reading files, starting server on port %d.", port)
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", port), nil))
}

func logRequest(r *http.Request, verbose bool) {
	if verbose {
		msg, _ := httputil.DumpRequest(r, true)
		log.Print("Incoming:\n\n" + string(msg))
	} else {
		uri := r.RequestURI
		if uri == "" {
			uri = r.URL.RequestURI()
		}
		log.Printf("%s %s %s (from %s)", r.Method, uri, r.Proto, r.RemoteAddr)
	}
}

// Citing Apple:
//
// The type values for the supported file formats are: audio/x-m4a, audio/mpeg,
// video/quicktime, video/mp4, video/x-m4v, and application/pdf.
var mimeType = map[string]string{
	".mp3": "audio/mpeg",
	".mp4": "audio/x-m4a",
	".m4a": "audio/x-m4a",
}

func GetPodcastItems(linkPrefix, dir string) []PodcastItem {
	assert(linkPrefix[len(linkPrefix)-1] == '/')
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
			tag, err := id3v2.ParseReader(f, id3v2.Options{
				Parse:       true,
				ParseFrames: []string{"Artist", "Title"},
			})
			if err != nil {
				log.Printf("Error opening %q: %v", path, err)
				// Keep going though, we just don't get any proper metadata.
			}
			title := tag.Title()
			if title == "" {
				title = name[:len(name)-len(ext)]
			}
			var desc string
			for _, comment := range tag.GetFrames(tag.CommonID("Comments")) {
				if c, ok := comment.(id3v2.CommentFrame); ok {
					desc += c.Text
				}
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			url, err := url.Parse(linkPrefix + path)
			if err != nil {
				return err
			}
			pp = append(pp, PodcastItem{
				Title: title,
				Link:  url.String(),
				Desc:  desc,
				Enclosure: Enclosure{
					Url:    url.String(),
					Length: info.Size(),
					Type:   mime,
				},
			})
		}
		return nil
	})
	ck(err)
	return pp
}
