package metrics

import "testing"

func TestFormatDuration(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0s"},
		{500, "0s"},
		{1500, "1s"},
		{34_000, "34s"},
		{60_000, "1m 00s"},
		{138_000, "2m 18s"},
		{3600_000, "1h 00m"},
		{3640_000, "1h 01m"},
	}
	for _, c := range cases {
		got := FormatDuration(c.in)
		if got != c.want {
			t.Errorf("FormatDuration(%d): got %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFormatTokens(t *testing.T) {
	cases := []struct {
		in   int
		want string
	}{
		{0, "0"},
		{42, "42"},
		{814, "814"},
		{999, "999"},
		{1000, "1.00k"},
		{12_400, "12.4k"},
		{1_200_000, "1.20M"},
	}
	for _, c := range cases {
		got := FormatTokens(c.in)
		if got != c.want {
			t.Errorf("FormatTokens(%d): got %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFormatCost(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	cases := []struct {
		name string
		in   *float64
		want string
	}{
		{"nil (unknown)", nil, "—"},
		{"zero (priced, no tokens)", f(0), "$0.00"},
		{"fractional cents", f(0.0428), "$0.043"},
		{"just below $1", f(0.9994), "$0.999"},
		{"rounding boundary $0.9995", f(0.9995), "$1.00"},
		{"round dollar", f(0.9999), "$1.00"},
		{"large", f(12.4), "$12.40"},
		{"tiny", f(0.0001), "$0.0001"},
	}
	for _, c := range cases {
		got := FormatCost(c.in)
		if got != c.want {
			t.Errorf("FormatCost(%s): got %q, want %q", c.name, got, c.want)
		}
	}
}
