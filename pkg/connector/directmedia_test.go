package connector

import (
	"bytes"
	"errors"
	"testing"

	"go.mau.fi/mautrix-slack/pkg/slackid"
)

func TestFileMediaIDRoundTrip(t *testing.T) {
	parsed, err := ParseMediaID(slackid.MakeFileMediaID("T05458EF8QN", "F0123ABCD"))
	if err != nil {
		t.Fatal(err)
	}
	file, ok := parsed.(*DirectMediaFile)
	if !ok || file.TeamID != "T05458EF8QN" || file.FileID != "F0123ABCD" {
		t.Errorf("got %#v", parsed)
	}
	if slackid.MakeFileMediaID("", "F1") != nil || slackid.MakeFileMediaID("T1", "") != nil {
		t.Error("empty team or file ID should give no media ID")
	}
	emoji := DirectMediaEmojiFromURL("https://emoji.slack-edge.com/T1/party/abc.png")
	if parsed, err = ParseMediaID(emoji.MediaID()); err != nil {
		t.Fatal(err)
	} else if _, ok = parsed.(*DirectMediaEmoji); !ok {
		t.Errorf("emoji IDs must still parse as emoji, got %#v", parsed)
	}
}

func TestHTMLCheckingWriter(t *testing.T) {
	var out bytes.Buffer
	hw := &htmlCheckingWriter{w: &out}
	if _, err := hw.Write([]byte("\n<!DOCTYPE html><html>login</html>")); !errors.Is(err, errHTMLDownload) {
		t.Errorf("an HTML page should be refused, got %v", err)
	}
	hw = &htmlCheckingWriter{w: &out}
	_, _ = hw.Write([]byte("\x89PNG"))
	_, _ = hw.Write([]byte("<!DOCTYPE html> later in the file is fine"))
	if hw.n != int64(out.Len()) || out.Len() == 0 {
		t.Errorf("counted %d bytes, wrote %d", hw.n, out.Len())
	}
}
