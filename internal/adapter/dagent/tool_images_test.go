package dagent

import (
	"encoding/base64"
	"testing"

	dachat "github.com/docker/docker-agent/pkg/chat"
	"github.com/docker/docker-agent/pkg/tools"
)

func TestToolResultImagesIncludesImagesAndImageDocuments(t *testing.T) {
	got := toolResultImages(&tools.ToolCallResult{
		Images: []tools.MediaContent{{Data: "aW1n", MimeType: "image/png"}},
		Documents: []tools.DocumentContent{
			{Name: "screenshot.jpg", Data: "c2hvdA==", MimeType: "image/jpeg"},
			{Name: "notes.txt", Data: "dGV4dA==", MimeType: "text/plain"},
		},
	})
	if len(got) != 2 {
		t.Fatalf("expected 2 images, got %#v", got)
	}
	if got[0].Name != "image-1" || got[0].MimeType != "image/png" || got[0].Data != "aW1n" {
		t.Fatalf("unexpected direct image: %#v", got[0])
	}
	if got[1].Name != "screenshot.jpg" || got[1].MimeType != "image/jpeg" {
		t.Fatalf("unexpected image document: %#v", got[1])
	}
}

func TestStoredToolImagesReadsLegacyAndDocumentParts(t *testing.T) {
	got := storedToolImages(dachat.Message{MultiContent: []dachat.MessagePart{
		{Type: dachat.MessagePartTypeImageURL, ImageURL: &dachat.MessageImageURL{URL: "data:image/webp;base64,d2VicA=="}},
		{Type: dachat.MessagePartTypeDocument, Document: &dachat.Document{
			Name: "stored.png", MimeType: "image/png",
			Source: dachat.DocumentSource{InlineData: []byte("png")},
		}},
	}})
	if len(got) != 2 {
		t.Fatalf("expected 2 stored images, got %#v", got)
	}
	if got[0].MimeType != "image/webp" || got[0].Data != "d2VicA==" {
		t.Fatalf("unexpected data URL image: %#v", got[0])
	}
	if got[1].Name != "stored.png" || got[1].Data != base64.StdEncoding.EncodeToString([]byte("png")) {
		t.Fatalf("unexpected document image: %#v", got[1])
	}
}

func TestImageFromDataURLRejectsUnsafeOrInvalidContent(t *testing.T) {
	for _, source := range []string{
		"https://example.com/image.png",
		"data:text/html;base64,PGgxPm5vPC9oMT4=",
		"data:image/png;base64,not base64",
	} {
		if _, ok := imageFromDataURL("image", source); ok {
			t.Fatalf("accepted invalid image source %q", source)
		}
	}
}
