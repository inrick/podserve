podserve
========

A simple podcast server. I rather often come across audio files that I'd like
to listen to as a podcast and this is a simple program to accomplish that. It
is very barebones: each podcast episode will be titled using the filename,  no
metadata tags are read. It supports mp3/m4a/mp4 files.


Usage
-----

To get started, run `podserve` with at least the following flags specified:

```shell
./podserve \
  -externalUrl "https://podcast.example.com/" \
  -dir "/media/podcast" \
  -port 8080 \
  -title "My Podcast"
```

(Where `/media/podcast` is your directory of media files.) Then point your
podcast client to `https://podcast.example.com/feed`. Naturally you have to
substitute the example address for one of your own, one that will be reachable
by your podcast client. It has to be specified so that the generated RSS feed
will in turn contain reachable addresses for the media.

(Depending on your podcast application, it might not be necessary for the URL
to be reachable externally. I had success accessing a podcast on the local
network using Apple Podcasts, but not using Overcast.)

Run `./podserve -help` for all available flags.

The server will reread the media file directory once every minute looking for
new files.
