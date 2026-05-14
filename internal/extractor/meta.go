package extractor

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// rawMeta mirrors the subset of yt-dlp -J fields we care about.  yt-dlp
// emits a deeply-nested JSON document; we deliberately keep this struct
// flat and forgiving so a missing field never breaks the pipeline.
type rawMeta struct {
	Title           string  `json:"title"`
	Uploader        string  `json:"uploader"`
	Channel         string  `json:"channel"`
	Duration        float64 `json:"duration"`
	Width           int     `json:"width"`
	Height          int     `json:"height"`
	FPS             float64 `json:"fps"`
	VCodec          string  `json:"vcodec"`
	ACodec          string  `json:"acodec"`
	Ext             string  `json:"ext"`
	FormatID        string  `json:"format_id"`
	Filesize        int64   `json:"filesize"`
	FilesizeApprox  int64   `json:"filesize_approx"`
	RequestedFormats []struct {
		FormatID string `json:"format_id"`
		VCodec   string `json:"vcodec"`
		ACodec   string `json:"acodec"`
		Ext      string `json:"ext"`
		Width    int    `json:"width"`
		Height   int    `json:"height"`
		FPS      float64 `json:"fps"`
		Filesize int64  `json:"filesize"`
	} `json:"requested_formats"`
}

func parseMetaJSON(data []byte) (Meta, error) {
	var r rawMeta
	if err := json.Unmarshal(data, &r); err != nil {
		return Meta{}, fmt.Errorf("decode yt-dlp metadata: %w", err)
	}
	m := Meta{
		Title:     r.Title,
		Uploader:  firstNonEmpty(r.Uploader, r.Channel),
		Duration:  time.Duration(r.Duration * float64(time.Second)),
		Container: r.Ext,
		FPS:       r.FPS,
		VCodec:    r.VCodec,
		ACodec:    r.ACodec,
		Filesize:  pickSize(r.Filesize, r.FilesizeApprox),
	}
	if r.Width > 0 && r.Height > 0 {
		m.Resolution = fmt.Sprintf("%dx%d", r.Width, r.Height)
	}
	// When yt-dlp picks two streams (video + audio) it surfaces them in
	// requested_formats; we use those for a cleaner v/a split readout.
	for _, f := range r.RequestedFormats {
		switch {
		case f.VCodec != "" && f.VCodec != "none":
			m.VideoFormat = f.FormatID
			if m.Resolution == "" && f.Width > 0 && f.Height > 0 {
				m.Resolution = fmt.Sprintf("%dx%d", f.Width, f.Height)
			}
			if m.VCodec == "" || m.VCodec == "none" {
				m.VCodec = f.VCodec
			}
			if m.FPS == 0 {
				m.FPS = f.FPS
			}
		case f.ACodec != "" && f.ACodec != "none":
			m.AudioFormat = f.FormatID
			if m.ACodec == "" || m.ACodec == "none" {
				m.ACodec = f.ACodec
			}
		}
	}
	if m.VideoFormat == "" {
		m.VideoFormat = r.FormatID
	}
	if m.Title == "" {
		return m, fmt.Errorf("yt-dlp metadata had no title")
	}
	return m, nil
}

// NeedsMux reports whether the chosen pipeline will require ffmpeg.
// True whenever yt-dlp will merge separate video + audio streams.
func (m Meta) NeedsMux() bool {
	return m.AudioFormat != "" && m.VideoFormat != "" && m.AudioFormat != m.VideoFormat
}

// SafeFilename renders a candidate output filename for display in the
// VCR plate.  yt-dlp resolves the real one — this is just for the UI.
func (m Meta) SafeFilename() string {
	t := strings.TrimSpace(m.Title)
	if t == "" {
		t = "video"
	}
	t = strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|':
			return '_'
		}
		return r
	}, t)
	if m.Container != "" {
		return t + "." + m.Container
	}
	return t + ".mp4"
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func pickSize(a, b int64) int64 {
	if a > 0 {
		return a
	}
	return b
}
