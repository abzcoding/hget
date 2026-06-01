package extractor

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// rawMeta mirrors the subset of yt-dlp -J fields we care about.  yt-dlp
// emits a deeply-nested JSON document; we deliberately keep this struct
// flat and forgiving so a missing field never breaks the pipeline.
type rawMeta struct {
	Title            string  `json:"title"`
	Uploader         string  `json:"uploader"`
	Channel          string  `json:"channel"`
	Duration         float64 `json:"duration"`
	Width            int     `json:"width"`
	Height           int     `json:"height"`
	FPS              float64 `json:"fps"`
	VCodec           string  `json:"vcodec"`
	ACodec           string  `json:"acodec"`
	Ext              string  `json:"ext"`
	FormatID         string  `json:"format_id"`
	Filesize         int64   `json:"filesize"`
	FilesizeApprox   int64   `json:"filesize_approx"`
	IsLive           bool    `json:"is_live"`
	RequestedFormats []struct {
		FormatID string  `json:"format_id"`
		VCodec   string  `json:"vcodec"`
		ACodec   string  `json:"acodec"`
		Ext      string  `json:"ext"`
		Width    int     `json:"width"`
		Height   int     `json:"height"`
		FPS      float64 `json:"fps"`
		Filesize int64   `json:"filesize"`
	} `json:"requested_formats"`
	Formats []rawFormat `json:"formats"`
}

type rawFormat struct {
	FormatID       string  `json:"format_id"`
	Ext            string  `json:"ext"`
	VCodec         string  `json:"vcodec"`
	ACodec         string  `json:"acodec"`
	Width          int     `json:"width"`
	Height         int     `json:"height"`
	FPS            float64 `json:"fps"`
	TBR            float64 `json:"tbr"`
	ABR            float64 `json:"abr"`
	VBR            float64 `json:"vbr"`
	Filesize       int64   `json:"filesize"`
	FilesizeApprox int64   `json:"filesize_approx"`
	FormatNote     string  `json:"format_note"`
	Protocol       string  `json:"protocol"`
}

// Format is the UI-facing description of one selectable yt-dlp format.
type Format struct {
	ID         string
	Ext        string
	Resolution string  // "1920x1080" or "" when audio-only
	Height     int     // sort key; 0 for audio-only
	FPS        float64
	VCodec     string
	ACodec     string
	TBR        float64 // total bitrate (kbps) — fallback sort key
	ABR        float64 // audio bitrate (kbps)
	Filesize   int64   // bytes
	Note       string  // format_note: "1080p60", "DRC", "Premium"
	HasVideo   bool
	HasAudio   bool
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
		IsLive:    r.IsLive,
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
	m.Formats = buildFormatTable(r.Formats)
	return m, nil
}

// buildFormatTable converts the raw formats[] slice into a cleaned,
// sorted Format slice.  Drops storyboards, image-only and HLS manifest
// rows (yt-dlp marks those with protocol prefixes like "mhtml").
func buildFormatTable(in []rawFormat) []Format {
	out := make([]Format, 0, len(in))
	for _, f := range in {
		// Storyboard rows have vcodec=acodec=none and an ext like "mhtml".
		if (f.VCodec == "" || f.VCodec == "none") &&
			(f.ACodec == "" || f.ACodec == "none") {
			continue
		}
		if strings.HasPrefix(f.Protocol, "mhtml") || f.Ext == "mhtml" {
			continue
		}
		ff := Format{
			ID:       f.FormatID,
			Ext:      f.Ext,
			Height:   f.Height,
			FPS:      f.FPS,
			VCodec:   f.VCodec,
			ACodec:   f.ACodec,
			TBR:      f.TBR,
			ABR:      f.ABR,
			Filesize: pickSize(f.Filesize, f.FilesizeApprox),
			Note:     f.FormatNote,
			HasVideo: f.VCodec != "" && f.VCodec != "none",
			HasAudio: f.ACodec != "" && f.ACodec != "none",
		}
		if f.Width > 0 && f.Height > 0 {
			ff.Resolution = fmt.Sprintf("%dx%d", f.Width, f.Height)
		}
		out = append(out, ff)
	}
	return out
}

// VideoFormats returns formats carrying a video stream, sorted from
// highest quality to lowest.  Progressive (video+audio combined)
// formats are included — they appear alongside video-only entries so
// the user can pick a one-shot download when it's offered.
func (m Meta) VideoFormats() []Format {
	var v []Format
	for _, f := range m.Formats {
		if f.HasVideo {
			v = append(v, f)
		}
	}
	sort.SliceStable(v, func(i, j int) bool {
		if v[i].Height != v[j].Height {
			return v[i].Height > v[j].Height
		}
		if v[i].FPS != v[j].FPS {
			return v[i].FPS > v[j].FPS
		}
		return v[i].TBR > v[j].TBR
	})
	return v
}

// AudioFormats returns audio-only formats, sorted by bitrate desc.
func (m Meta) AudioFormats() []Format {
	var a []Format
	for _, f := range m.Formats {
		if f.HasAudio && !f.HasVideo {
			a = append(a, f)
		}
	}
	sort.SliceStable(a, func(i, j int) bool {
		ai, aj := a[i].ABR, a[j].ABR
		if ai == 0 {
			ai = a[i].TBR
		}
		if aj == 0 {
			aj = a[j].TBR
		}
		return ai > aj
	})
	return a
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
