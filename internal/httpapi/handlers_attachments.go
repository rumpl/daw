package httpapi

import (
	"fmt"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/rumpl/daw/internal/protocol"
)

const (
	maxAttachmentBytes = 10 << 20
	maxAttachmentCount = 4
)

func (s *Server) handleAttachment(w http.ResponseWriter, r *http.Request) {
	c, ok := s.mustChat(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxAttachmentBytes+(1<<20))
	if err := r.ParseMultipartForm(maxAttachmentBytes); err != nil {
		s.fail(w, http.StatusRequestEntityTooLarge, "attachment_too_large", "the attachment exceeds 10 MB")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		s.fail(w, http.StatusBadRequest, "missing_attachment", "a file is required")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxAttachmentBytes+1))
	if err != nil || len(data) > maxAttachmentBytes {
		s.fail(w, http.StatusRequestEntityTooLarge, "attachment_too_large", "the attachment exceeds 10 MB")
		return
	}
	if len(data) == 0 {
		s.fail(w, http.StatusBadRequest, "empty_attachment", "the attachment is empty")
		return
	}
	name := filepath.Base(strings.TrimSpace(header.Filename))
	if name == "." || name == "" {
		name = "attachment"
	}
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, name)
	if len(name) > 200 {
		name = name[:200]
	}
	mimeType := attachmentMime(name, data)
	if mimeType == "" {
		s.fail(w, http.StatusUnsupportedMediaType, "unsupported_attachment", "only text files, PDF, JPEG, PNG, GIF, and WebP are supported")
		return
	}

	meta := protocol.Attachment{ID: newOpaqueID("att"), Name: name, MimeType: mimeType, Size: int64(len(data))}
	c.mu.Lock()
	if len(c.attachments) >= maxAttachmentCount {
		c.mu.Unlock()
		s.fail(w, http.StatusConflict, "too_many_attachments", fmt.Sprintf("a chat may have at most %d pending attachments", maxAttachmentCount))
		return
	}
	c.attachments[meta.ID] = uploadedAttachment{meta: meta, data: data}
	c.mu.Unlock()
	s.json(w, http.StatusCreated, meta)
}

func (s *Server) handleDeleteAttachment(w http.ResponseWriter, r *http.Request) {
	c, ok := s.mustChat(w, r)
	if !ok {
		return
	}
	id := r.PathValue("attachmentId")
	c.mu.Lock()
	_, exists := c.attachments[id]
	delete(c.attachments, id)
	c.mu.Unlock()
	if !exists {
		s.fail(w, http.StatusNotFound, "unknown_attachment", "unknown attachment")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func attachmentMime(name string, data []byte) string {
	detected := strings.SplitN(http.DetectContentType(data), ";", 2)[0]
	switch detected {
	case "image/jpeg", "image/png", "image/gif", "image/webp", "application/pdf":
		return detected
	}
	if !utf8.Valid(data) || strings.IndexByte(string(data), 0) >= 0 {
		return ""
	}
	if extType := mime.TypeByExtension(strings.ToLower(filepath.Ext(name))); strings.HasPrefix(extType, "text/") {
		return strings.SplitN(extType, ";", 2)[0]
	}
	return "text/plain"
}
