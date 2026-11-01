package search

import (
	"context"
	"path/filepath"
	"testing"
)

func TestBinaryHintDoesNotReadAndHonorsEvidence(t *testing.T) {
	abs := filepath.Join(t.TempDir(), "missing.unknown")
	if BinaryHint(abs, "missing.unknown", 100, true) {
		t.Fatal("unknown guessed binary")
	}
	if !BinaryHint(abs, "missing.jar", 100, true) {
		t.Fatal("known filename missing")
	}
	if BinaryHint(abs, "empty.jar", 0, true) {
		t.Fatal("empty file is text")
	}
	invocationClassifications.classify(context.Background(), abs, nil, func(context.Context, string, []byte) fileClassification {
		return fileClassification{class: contentBinary}
	})
	if !BinaryHint(abs, "missing.unknown", 100, true) {
		t.Fatal("lost existing evidence")
	}
	textAbs := filepath.Join(t.TempDir(), "text.jar")
	invocationClassifications.classify(context.Background(), textAbs, nil, func(context.Context, string, []byte) fileClassification {
		return fileClassification{class: contentText}
	})
	if BinaryHint(textAbs, "text.jar", 100, true) {
		t.Fatal("name overrode content evidence")
	}
}
