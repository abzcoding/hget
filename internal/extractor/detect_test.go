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

func TestFormatSelection_ArgsDefaults(t *testing.T) {
	got := (FormatSelection{}).Args()
	want := []string{"-f", "bv*+ba/b", "--merge-output-format", "mp4"}
	if len(got) != len(want) {
		t.Fatalf("default args length: got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("default args[%d]=%q want %q", i, got[i], want[i])
		}
	}
}

func TestFormatSelection_ArgsExplicit(t *testing.T) {
	got := FormatSelection{Spec: "299+140", Container: "mkv"}.Args()
	if got[1] != "299+140" || got[3] != "mkv" {
		t.Errorf("explicit args=%v", got)
	}
}

func TestFormatPreference_AdaptiveSpec(t *testing.T) {
	cases := []struct {
		name string
		pref FormatPreference
		want string
	}{
		{
			name: "zero falls back to universal default",
			pref: FormatPreference{},
			want: "bv*+ba/b",
		},
		{
			name: "height only",
			pref: FormatPreference{HeightCeiling: 720},
			want: "bv[height<=720]+ba/bv[height<=720]+ba/bv+ba/b",
		},
		{
			name: "height + codec + abr cap",
			pref: FormatPreference{HeightCeiling: 1080, VCodec: "avc1", ABRCeiling: 160},
			want: "bv[height<=1080][vcodec~='^avc1']+ba[abr<=160]/bv[height<=1080]+ba/bv+ba/b",
		},
		{
			name: "progressive single-stream pick",
			pref: FormatPreference{HeightCeiling: 720, Progressive: true},
			want: "b[height<=720]/best",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.pref.AdaptiveSpec(); got != tc.want {
				t.Errorf("AdaptiveSpec = %q\nwant         %q", got, tc.want)
			}
		})
	}
}

func TestQualityPreset_KnownNames(t *testing.T) {
	cases := []struct {
		name        string
		wantHeight  int
		wantSpec    string
		wantContainer string
	}{
		{"720p", 720, "", "mp4"},
		{"1080p", 1080, "", "mp4"},
		{"4K", 2160, "", "mp4"},
		{"best", 0, "", "mp4"},
		{"audio", 0, "ba/bestaudio", "mp4"},
		{"unknown", 0, "", "mp4"},
		{"", 0, "", "mp4"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := QualityPreset(tc.name, "")
			if got.Container != tc.wantContainer {
				t.Errorf("Container=%q want %q", got.Container, tc.wantContainer)
			}
			if got.Spec != tc.wantSpec {
				t.Errorf("Spec=%q want %q", got.Spec, tc.wantSpec)
			}
			if got.Pref.HeightCeiling != tc.wantHeight {
				t.Errorf("HeightCeiling=%d want %d", got.Pref.HeightCeiling, tc.wantHeight)
			}
		})
	}
}

func TestQualityPreset_CustomContainer(t *testing.T) {
	got := QualityPreset("1080p", "mkv")
	if got.Container != "mkv" {
		t.Errorf("Container=%q want mkv", got.Container)
	}
}

func TestOptions_SortArgs_LangPref(t *testing.T) {
	got := Options{LangPref: "en"}.sortArgs()
	want := []string{"-S", "lang:en"}
	for i, w := range want {
		if i >= len(got) || got[i] != w {
			t.Errorf("sortArgs[%d]=%q want %q (got=%v)", i, got[i], w, got)
		}
	}
}

func TestOptions_SortArgs_EmptyPrefDisabled(t *testing.T) {
	if got := (Options{}).sortArgs(); len(got) != 0 {
		t.Errorf("empty LangPref should produce no -S args, got %v", got)
	}
}

func TestFormatSelection_ArgsUseAdaptiveWhenPrefSet(t *testing.T) {
	// When Pref is non-zero, the adaptive expression wins regardless
	// of any Spec — guarantees cross-tape fallback inside the batch
	// FormatAll pipeline.
	got := FormatSelection{
		Spec:      "299+140", // would normally win
		Container: "mp4",
		Pref:      FormatPreference{HeightCeiling: 1080, VCodec: "avc1"},
	}.Args()
	want := "bv[height<=1080][vcodec~='^avc1']+ba/bv[height<=1080]+ba/bv+ba/b"
	if got[1] != want {
		t.Errorf("Args[1] = %q, want %q", got[1], want)
	}
}

func TestParseMetaJSON_ExtractsFormats(t *testing.T) {
	doc := []byte(`{
        "title": "Sample",
        "ext": "mp4",
        "duration": 120,
        "formats": [
            {"format_id": "sb0", "ext": "mhtml", "vcodec": "none", "acodec": "none", "protocol": "mhtml"},
            {"format_id": "140", "ext": "m4a", "vcodec": "none", "acodec": "mp4a.40.2", "abr": 128, "filesize": 5000000},
            {"format_id": "299", "ext": "mp4", "vcodec": "avc1.640028", "acodec": "none", "width": 1920, "height": 1080, "fps": 60, "tbr": 6000, "filesize": 80000000, "format_note": "1080p60"},
            {"format_id": "22",  "ext": "mp4", "vcodec": "avc1.64001F", "acodec": "mp4a.40.2", "width": 1280, "height": 720, "fps": 30}
        ]
    }`)
	m, err := parseMetaJSON(doc)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(m.Formats) != 3 {
		t.Fatalf("expected 3 cleaned formats (mhtml dropped), got %d: %+v", len(m.Formats), m.Formats)
	}
	v := m.VideoFormats()
	if len(v) != 2 || v[0].ID != "299" {
		t.Errorf("video sort: got %+v", v)
	}
	a := m.AudioFormats()
	if len(a) != 1 || a[0].ID != "140" {
		t.Errorf("audio-only filter: got %+v", a)
	}
	// Progressive format must carry HasAudio so the UI can collapse the
	// audio rocker into "(included)".
	for _, f := range v {
		if f.ID == "22" && !f.HasAudio {
			t.Errorf("progressive format 22 missing HasAudio flag")
		}
	}
}
