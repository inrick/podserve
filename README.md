podserve
========

A simple podcast server. I rather often come across audio files that I'd like
to listen to as a podcast. This is a simple program to accomplish that. It is
very barebones: each podcast episode will be titled using the filename, for
example. No metadata tags are read. It supports mp3/m4a/mp4 files.


Usage
-----

Run `./podserve -help` for all available flags. To get started, run `podserve`
with at least the following flags specified:

```
./podserve \
  -externalUrl <external base URL where server is exposed, e.g., https://podcast.example.com/> \
  -dir <path to directory with media files> \
  -port <port to listen on> \
  -title <podcast title>
```

and then point your podcast app to `<external url>/feed`.

It will reread the media file directory once every minute looking for new
files.

Depending on your podcast application, it might not be necessary to use an
externally reachable URL. I had success accessing a podcast on the local
network using Apple Podcasts, but not using Overcast.
