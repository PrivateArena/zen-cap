// [VERIFIED]
package capture

import (
	"image/color"
	"testing"
)

func TestFormatColor(t *testing.T) {
	tests := []struct {
		name     string
		color    color.Color
		format   string
		expected string
	}{
		{
			name:     "hex standard",
			color:    color.RGBA{R: 255, G: 0, B: 127, A: 255},
			format:   "hex",
			expected: "#ff007f",
		},
		{
			name:     "hex default",
			color:    color.RGBA{R: 0, G: 255, B: 0, A: 255},
			format:   "",
			expected: "#00ff00",
		},
		{
			name:     "rgb",
			color:    color.RGBA{R: 10, G: 20, B: 30, A: 255},
			format:   "rgb",
			expected: "rgb(10, 20, 30)",
		},
		{
			name:     "rgba",
			color:    color.NRGBA{R: 10, G: 20, B: 30, A: 128},
			format:   "rgba",
			expected: "rgba(10, 20, 30, 0.50)",
		},
		{
			name:     "hsl red",
			color:    color.RGBA{R: 255, G: 0, B: 0, A: 255},
			format:   "hsl",
			expected: "hsl(0, 100%, 50%)",
		},
		{
			name:     "hsl green",
			color:    color.RGBA{R: 0, G: 255, B: 0, A: 255},
			format:   "hsl",
			expected: "hsl(120, 100%, 50%)",
		},
		{
			name:     "hsl blue",
			color:    color.RGBA{R: 0, G: 0, B: 255, A: 255},
			format:   "hsl",
			expected: "hsl(240, 100%, 50%)",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			actual := formatColor(tc.color, tc.format)
			if actual != tc.expected {
				t.Errorf("expected %q, got %q", tc.expected, actual)
			}
		})
	}
}
