package tracing

import (
	"strings"
	"testing"
)

func TestSamplerFor(t *testing.T) {
	cases := []struct {
		ratio float64
		want  string // substring of sampler description
	}{
		{0, "AlwaysOnSampler"},
		{1, "AlwaysOnSampler"},
		{1.5, "AlwaysOnSampler"},
		{0.1, "TraceIDRatioBased"},
	}
	for _, c := range cases {
		got := samplerFor(c.ratio).Description()
		if !strings.Contains(got, c.want) {
			t.Errorf("samplerFor(%v) = %q, want substring %q", c.ratio, got, c.want)
		}
	}
}
