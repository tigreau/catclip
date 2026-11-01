package platform

import (
	"reflect"
	"testing"
)

func TestStartLoadingSpinnerNoopStopIsIdempotent(t *testing.T) {
	stop := StartLoadingSpinner(nil, "test")
	stop()
	stop()
}

func TestSpinnerHintLines(t *testing.T) {
	tests := []struct {
		name string
		hint string
		want []string
	}{
		{name: "multiline", hint: "first\nsecond\n", want: []string{"first", "second"}},
		{name: "empty", hint: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := spinnerHintLines(tt.hint); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("spinnerHintLines() = %#v, want %#v", got, tt.want)
			}
		})
	}
}
