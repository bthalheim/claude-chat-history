package main

import (
	"os"
	"testing"
)

func TestLoadItems(t *testing.T) {
	dir := "../chat-history"
	if _, err := os.Stat(dir); err != nil {
		t.Skip("no chat-history dir")
	}
	items, err := loadItems(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		t.Logf("%s md=%v n=%d title=%q", it.path, it.isMD, it.count, it.title)
		if !it.isMD && it.count == 0 {
			t.Errorf("%s parsed zero messages", it.path)
		}
	}
}
