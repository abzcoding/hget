package extractor

import "testing"

func TestLooksExtractable(t *testing.T) {
	cases := []struct {
		url  string
		want bool
	}{
		{"https://www.youtube.com/watch?v=abc", true},
		{"https://youtu.be/abc", true},
		{"https://music.youtube.com/watch?v=abc", true},
		{"https://m.youtube.com/watch?v=abc", true},
		{"https://vimeo.com/12345", true},
		{"https://www.twitch.tv/clips/abc", true},
		{"https://soundcloud.com/artist/track", true},
		{"https://example.com/file.iso", false},
		{"https://releases.ubuntu.com/24.04/ubuntu.iso", false},
		{"not a url", false},
		{"", false},
	}
	for _, c := range cases {
		if got := LooksExtractable(c.url); got != c.want {
			t.Errorf("LooksExtractable(%q)=%v want %v", c.url, got, c.want)
		}
	}
}

func TestParseProgressLine(t *testing.T) {
	line := "HGET| 12.5%|1048576|8388608|524288|45|3|10"
	p, ok := parseProgressLine(line)
	if !ok {
		t.Fatal("expected parse ok")
	}
	if p.Percent != 12.5 {
		t.Errorf("percent=%v want 12.5", p.Percent)
	}
	if p.Downloaded != 1048576 {
		t.Errorf("downloaded=%d want 1048576", p.Downloaded)
	}
	if p.Total != 8388608 {
		t.Errorf("total=%d want 8388608", p.Total)
	}
	if p.SpeedBPS != 524288 {
		t.Errorf("speed=%v want 524288", p.SpeedBPS)
	}
	if p.ETA.Seconds() != 45 {
		t.Errorf("eta=%v want 45s", p.ETA)
	}
	if p.Fragment != 3 || p.FragmentN != 10 {
		t.Errorf("fragment=%d/%d want 3/10", p.Fragment, p.FragmentN)
	}
}

func TestParseProgressLineMalformed(t *testing.T) {
	if _, ok := parseProgressLine("HGET|nope"); ok {
		t.Error("expected parse failure on short line")
	}
}

func TestParseProgressLineNAFields(t *testing.T) {
	line := "HGET| 50.0%|500|NA|None|NA|NA|NA"
	p, ok := parseProgressLine(line)
	if !ok {
		t.Fatal("expected ok despite NA fields")
	}
	if p.Total != 0 || p.SpeedBPS != 0 {
		t.Errorf("expected zeros for NA fields; got total=%d speed=%v", p.Total, p.SpeedBPS)
	}
}

func TestMetaNeedsMux(t *testing.T) {
	if !(Meta{VideoFormat: "248", AudioFormat: "251"}).NeedsMux() {
		t.Error("split v+a should need mux")
	}
	if (Meta{VideoFormat: "22", AudioFormat: "22"}).NeedsMux() {
		t.Error("identical format ids = single stream, no mux")
	}
	if (Meta{VideoFormat: "22"}).NeedsMux() {
		t.Error("video-only should not need mux")
	}
}

func TestMetaSafeFilename(t *testing.T) {
	m := Meta{Title: "Hello / World", Container: "mp4"}
	got := m.SafeFilename()
	want := "Hello _ World.mp4"
	if got != want {
		t.Errorf("SafeFilename=%q want %q", got, want)
	}
}
