package main

import (
	"bytes"
	"html/template"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

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
 <title>{{.Metadata.Title}}</title>
 <link>{{.Metadata.Link}}</link>
 <description>{{.Metadata.Desc}}</description>
 <language>{{.Metadata.Language}}</language>
 <itunes:image href="{{.Metadata.CoverUrl}}" />
 {{range .Items}}
 <item>
  <title>{{.Title}}</title>
  <link>{{.Link}}</link>
  <description>{{.Desc}}</description>
  <pubDate>{{timeRFC2822 .ModTime}}</pubDate>
  <enclosure url="{{.Enclosure.Url}}" length="{{.Enclosure.Length}}" Type="{{.Enclosure.Type}}" />
 </item>
 {{- end}}
</channel>
</rss>
`
)

// The date format required in a podcast RSS. See [2] in package documentation.
const TimeRFC2822 = "Mon, Jan 02 2006 15:04:05 MST"

type TemplateData struct {
	Metadata Metadata
	Items    []Item
}

type Metadata struct {
	Title    string
	Link     string
	Desc     string
	Language string
	CoverUrl string

	externalUrl string
	localRoot   string
}

type Item struct {
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

type FileInfo struct {
	Path     string
	MimeType string
	Size     int64
	ModTime  time.Time
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

func (m Metadata) GenerateFeed() (feedXml []byte, files map[string]FileInfo, err error) {
	items, err := m.Items()
	if err != nil {
		return nil, nil, err
	}
	feedXml, err = m.Feed(items)
	if err != nil {
		return nil, nil, err
	}
	files = make(map[string]FileInfo)
	for _, it := range items {
		files[it.Path] = FileInfo{
			Path:     filepath.Join(m.localRoot, it.Path),
			MimeType: it.Enclosure.Type,
			Size:     it.Enclosure.Length,
			ModTime:  it.ModTime,
		}
	}
	return feedXml, files, nil
}

// Reads the local file system and returns a slice of available Items
// with all the metadata required to serve them.
func (m Metadata) Items() ([]Item, error) {
	if m.externalUrl[len(m.externalUrl)-1] != '/' {
		panic("Meta.Items: expected externalUrl to end in '/'")
	}
	var pp []Item
	fsys := os.DirFS(m.localRoot)
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
			f, err := os.Open(filepath.Join(m.localRoot, path))
			if err != nil {
				return err
			}
			defer f.Close()
			title := name[:len(name)-len(ext)]
			info, err := d.Info()
			if err != nil {
				return err
			}
			url, err := url.Parse(m.externalUrl + url.PathEscape(path))
			if err != nil {
				return err
			}
			pp = append(pp, Item{
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

func (m Metadata) Feed(items []Item) ([]byte, error) {
	ff := template.FuncMap{
		"timeRFC2822": func(t *time.Time) string {
			return t.Format(TimeRFC2822)
		},
	}
	tmpl := template.Must(template.New("rss").Funcs(ff).Parse(RSSTemplate))
	var buf bytes.Buffer
	buf.Write([]byte(XMLHeader))
	err := tmpl.Execute(&buf, TemplateData{m, items})
	return buf.Bytes(), err
}
