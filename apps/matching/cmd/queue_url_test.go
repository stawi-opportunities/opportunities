package main

import "testing"

func TestQueuePublisherURL(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		queueURL   string
		publishURL string
		gcpProject string
		want       string
	}{
		{
			name:       "explicit publish wins",
			queueURL:   "push://opportunities-cv-embed?protocol=gcppubsub",
			publishURL: "gcppubsub://stawi-opportunities/opportunities-cv-embed",
			want:       "gcppubsub://stawi-opportunities/opportunities-cv-embed",
		},
		{
			name:       "derive gcppubsub from push host",
			queueURL:   "push://opportunities-cv-embed?protocol=gcppubsub",
			gcpProject: "stawi-opportunities",
			want:       "gcppubsub://stawi-opportunities/opportunities-cv-embed",
		},
		{
			name:     "mem pass-through",
			queueURL: "mem://svc.opportunities.matching.cv.extract.v1",
			want:     "mem://svc.opportunities.matching.cv.extract.v1",
		},
		{
			name:     "gcppubsub pass-through",
			queueURL: "gcppubsub://stawi-opportunities/opportunities-cv-embed",
			want:     "gcppubsub://stawi-opportunities/opportunities-cv-embed",
		},
		{
			name:     "push without project falls back to mem",
			queueURL: "push://opportunities-cv-embed",
			want:     "mem://opportunities-cv-embed",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := queuePublisherURL(tc.queueURL, tc.publishURL, tc.gcpProject)
			if got != tc.want {
				t.Fatalf("queuePublisherURL() = %q, want %q", got, tc.want)
			}
		})
	}
}
