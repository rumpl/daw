package dagent

import (
	"encoding/base64"
	"fmt"
	"strings"

	dachat "github.com/docker/docker-agent/pkg/chat"
	"github.com/docker/docker-agent/pkg/tools"

	"github.com/rumpl/daw/internal/protocol"
)

func toolResultImages(result *tools.ToolCallResult) []protocol.ToolImage {
	if result == nil {
		return nil
	}
	images := make([]protocol.ToolImage, 0, len(result.Images)+len(result.Documents))
	for i, image := range result.Images {
		if dachat.IsImageMimeType(image.MimeType) && image.Data != "" {
			images = append(images, protocol.ToolImage{
				Name: fmt.Sprintf("image-%d", i+1), MimeType: image.MimeType, Data: image.Data,
			})
		}
	}
	for _, document := range result.Documents {
		if !dachat.IsImageMimeType(document.MimeType) || document.Data == "" {
			continue
		}
		name := document.Name
		if name == "" {
			name = "image"
		}
		images = append(images, protocol.ToolImage{Name: name, MimeType: document.MimeType, Data: document.Data})
	}
	return images
}

func storedToolImages(message dachat.Message) []protocol.ToolImage {
	var images []protocol.ToolImage
	for i, part := range message.MultiContent {
		switch {
		case part.ImageURL != nil:
			if image, ok := imageFromDataURL(fmt.Sprintf("image-%d", i+1), part.ImageURL.URL); ok {
				images = append(images, image)
			}
		case part.Document != nil && dachat.IsImageMimeType(part.Document.MimeType) && len(part.Document.Source.InlineData) > 0:
			name := part.Document.Name
			if name == "" {
				name = "image"
			}
			images = append(images, protocol.ToolImage{
				Name: name, MimeType: part.Document.MimeType,
				Data: base64.StdEncoding.EncodeToString(part.Document.Source.InlineData),
			})
		}
	}
	return images
}

func imageFromDataURL(name, source string) (protocol.ToolImage, bool) {
	header, data, ok := strings.Cut(source, ",")
	if !ok || !strings.HasPrefix(header, "data:") || !strings.HasSuffix(header, ";base64") || data == "" {
		return protocol.ToolImage{}, false
	}
	mimeType := strings.TrimSuffix(strings.TrimPrefix(header, "data:"), ";base64")
	if !dachat.IsImageMimeType(mimeType) {
		return protocol.ToolImage{}, false
	}
	if _, err := base64.StdEncoding.DecodeString(data); err != nil {
		return protocol.ToolImage{}, false
	}
	return protocol.ToolImage{Name: name, MimeType: mimeType, Data: data}, true
}
