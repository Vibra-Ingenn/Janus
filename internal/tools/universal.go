package tools

// Universal ingestion + output tools. These are the "any input / any output"
// layer that lets Janus deal with messy real-world medical data (scanned
// faxes, Word docs, images, etc.) and emit polished documents back out.
//
// Design notes:
//   - Pure Go wherever possible. Only ocr_extract and image_extract shell out
//     to tesseract via run_command, because there is no viable pure-Go OCR.
//   - Every tool here is best-effort and degrades gracefully. A failure in
//     one parser does not take down the kernel.
//   - Output renderers always write to workspace/outputs/ with a timestamped
//     filename so parallel calls never clobber each other.

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/go-pdf/fpdf"
)

const outputDir = "workspace/outputs"

// ═══════════════════════════════════════════════════════════════════════════
// registerUniversalTools — plugs the 6 universal tools into the registry.
// ═══════════════════════════════════════════════════════════════════════════

func (r *Registry) registerUniversalTools() {
	r.Register(ToolDef{
		Name:        "auto_ingest",
		Description: "Detects a file's type by magic bytes and extension, then routes it to the correct parser (PDF, DOCX, HL7, image OCR, plain text). Use this when you do not know what kind of file the user uploaded.",
		Parameters: map[string]string{
			"path": "Path to any file. Supported: PDF, DOCX, TXT/MD/CSV/JSON/XML/HL7, PNG/JPG (OCR).",
		},
	}, toolAutoIngest)

	r.Register(ToolDef{
		Name:        "ocr_extract",
		Description: "Runs Tesseract OCR on a scanned PDF or image file. Use this when pdf_extract returned '(no text found)' — the PDF is a scanned fax or image-only.",
		Parameters: map[string]string{
			"path":     "Path to a PDF or image file (PNG, JPG, TIFF, BMP).",
			"language": "(optional) Tesseract language code, e.g. 'eng', 'spa', 'eng+spa'. Default 'eng'.",
		},
	}, toolOCRExtract)

	r.Register(ToolDef{
		Name:        "docx_extract",
		Description: "Extracts plain text from a Microsoft Word .docx file using pure Go (no external tools). Use this for Word documents, referral letters, transcribed notes.",
		Parameters: map[string]string{
			"path": "Path to a .docx file.",
		},
	}, toolDOCXExtract)

	r.Register(ToolDef{
		Name:        "image_extract",
		Description: "Runs OCR on an image file (PNG, JPG, TIFF, BMP) using Tesseract. Use this for photos of documents, handwritten notes, whiteboards, etc.",
		Parameters: map[string]string{
			"path":     "Path to an image file.",
			"language": "(optional) Tesseract language code, default 'eng'.",
		},
	}, toolImageExtract)

	r.Register(ToolDef{
		Name:        "render_pdf",
		Description: "Generates a formatted PDF file from plain text or simple Markdown. Writes to workspace/outputs/ and returns the path. Use this when the user asks for a polished PDF output (letter, report, discharge summary).",
		Parameters: map[string]string{
			"content":  "The text or Markdown content to render.",
			"title":    "(optional) Document title shown at top of the page.",
			"filename": "(optional) Base filename without extension. Default: 'document'.",
		},
	}, toolRenderPDF)

	r.Register(ToolDef{
		Name:        "render_docx",
		Description: "Generates a Microsoft Word .docx file from plain text. Writes to workspace/outputs/ and returns the path. Use this when the user wants an editable Word document.",
		Parameters: map[string]string{
			"content":  "The text content to write into the document.",
			"title":    "(optional) Document title shown at top as a heading.",
			"filename": "(optional) Base filename without extension. Default: 'document'.",
		},
	}, toolRenderDOCX)
}

// ═══════════════════════════════════════════════════════════════════════════
// auto_ingest — Universal file-type router
// ═══════════════════════════════════════════════════════════════════════════

func toolAutoIngest(args map[string]any) ToolResult {
	path, _ := args["path"].(string)
	if path == "" {
		return ToolResult{ToolName: "auto_ingest", Success: false, Error: "path is required"}
	}
	if !filepath.IsAbs(path) {
		if abs, err := filepath.Abs(path); err == nil {
			path = abs
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		return ToolResult{ToolName: "auto_ingest", Success: false, Error: fmt.Sprintf("cannot stat %s: %v", path, err)}
	}
	if info.IsDir() {
		return ToolResult{ToolName: "auto_ingest", Success: false, Error: "path is a directory, not a file"}
	}

	kind, err := detectFileType(path)
	if err != nil {
		return ToolResult{ToolName: "auto_ingest", Success: false, Error: fmt.Sprintf("cannot detect file type: %v", err)}
	}

	log.Printf("auto_ingest: %s detected as %s (size=%d)", filepath.Base(path), kind, info.Size())

	// Route to the appropriate extractor. Every branch returns a ToolResult
	// tagged as "auto_ingest" with the underlying tool's output, so the AI
	// can see which path was taken.
	switch kind {
	case "pdf":
		return ToolResult{ToolName: "auto_ingest", Success: false, Error: "PDF extraction not yet implemented in this build"}
	case "docx":
		res := toolDOCXExtract(map[string]any{"path": path})
		return wrapIngest("docx", res)
	case "image":
		res := toolImageExtract(map[string]any{"path": path})
		return wrapIngest("image", res)
	case "hl7":
		return ToolResult{ToolName: "auto_ingest", Success: false, Error: "HL7 parsing not yet implemented in this build"}
	case "text":
		// Plain text, CSV, JSON, XML, MD — read directly with size cap.
		data, err := readCapped(path, 128*1024)
		if err != nil {
			return ToolResult{ToolName: "auto_ingest", Success: false, Error: fmt.Sprintf("read text: %v", err)}
		}
		return ToolResult{
			ToolName: "auto_ingest",
			Success:  true,
			Output:   fmt.Sprintf("[detected=text size=%d]\n%s", info.Size(), string(data)),
		}
	default:
		return ToolResult{
			ToolName: "auto_ingest",
			Success:  false,
			Error:    fmt.Sprintf("unsupported file type %q — try read_file for raw bytes or tell the user the format is not supported", kind),
		}
	}
}

// wrapIngest rewrites a sub-tool's result to look like an auto_ingest result
// so the AI sees a consistent tool name in the conversation history.
func wrapIngest(detected string, res ToolResult) ToolResult {
	prefix := fmt.Sprintf("[detected=%s via=%s]\n", detected, res.ToolName)
	return ToolResult{
		ToolName: "auto_ingest",
		Success:  res.Success,
		Output:   prefix + res.Output,
		Error:    res.Error,
	}
}

// detectFileType sniffs magic bytes with a filename extension fallback.
// Returns one of: pdf, docx, image, hl7, text, unknown.
func detectFileType(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	// Read enough bytes to cover every magic header we care about.
	head := make([]byte, 512)
	n, _ := f.Read(head)
	head = head[:n]

	// Magic bytes take precedence over extension because users rename files.
	switch {
	case bytes.HasPrefix(head, []byte("%PDF-")):
		return "pdf", nil
	case bytes.HasPrefix(head, []byte{0x50, 0x4B, 0x03, 0x04}): // ZIP header — docx/xlsx/pptx
		// Differentiate by extension since they share the ZIP header.
		ext := strings.ToLower(filepath.Ext(path))
		if ext == ".docx" {
			return "docx", nil
		}
		return "unknown", nil
	case bytes.HasPrefix(head, []byte{0xFF, 0xD8, 0xFF}): // JPEG
		return "image", nil
	case bytes.HasPrefix(head, []byte{0x89, 0x50, 0x4E, 0x47}): // PNG
		return "image", nil
	case bytes.HasPrefix(head, []byte{0x47, 0x49, 0x46}): // GIF
		return "image", nil
	case bytes.HasPrefix(head, []byte{0x42, 0x4D}): // BMP
		return "image", nil
	}

	// HL7 v2 messages start with "MSH|".
	if bytes.HasPrefix(bytes.TrimLeft(head, "\r\n\t "), []byte("MSH|")) {
		return "hl7", nil
	}

	// If it's mostly printable ASCII/UTF-8, treat as text.
	if isProbablyText(head) {
		return "text", nil
	}
	return "unknown", nil
}

// isProbablyText returns true if the buffer is at least 90% printable / UTF-8.
func isProbablyText(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	printable := 0
	for _, c := range b {
		if c == '\t' || c == '\n' || c == '\r' || (c >= 0x20 && c < 0x7F) || c >= 0x80 {
			printable++
		}
	}
	return float64(printable)/float64(len(b)) > 0.90
}

func readCapped(path string, max int) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, int64(max)))
}

// ═══════════════════════════════════════════════════════════════════════════
// ocr_extract — Tesseract wrapper for scanned PDFs & images
// ═══════════════════════════════════════════════════════════════════════════

func toolOCRExtract(args map[string]any) ToolResult {
	path, _ := args["path"].(string)
	if path == "" {
		return ToolResult{ToolName: "ocr_extract", Success: false, Error: "path is required"}
	}
	lang, _ := args["language"].(string)
	if lang == "" {
		lang = "eng"
	}
	if !filepath.IsAbs(path) {
		if abs, err := filepath.Abs(path); err == nil {
			path = abs
		}
	}
	if _, err := os.Stat(path); err != nil {
		return ToolResult{ToolName: "ocr_extract", Success: false, Error: fmt.Sprintf("file not found: %s", path)}
	}

	// Locate tesseract on PATH. If missing, give the user install instructions
	// instead of a cryptic exec failure.
	if _, err := exec.LookPath("tesseract"); err != nil {
		return ToolResult{
			ToolName: "ocr_extract",
			Success:  false,
			Error:    "tesseract not installed on PATH. Install from https://github.com/UB-Mannheim/tesseract/wiki (Windows) or 'apt install tesseract-ocr' (Linux). After install, restart Janus.",
		}
	}

	// For PDFs we need to rasterize each page first — tesseract cannot read
	// PDFs directly unless compiled with Leptonica PDF support. We shell out
	// to `tesseract` with the PDF path; modern Windows builds DO support it.
	// If that fails, we fall back to informing the user.
	return runTesseract(path, lang, "ocr_extract")
}

// toolImageExtract is the same code path as OCR but named differently so
// the AI picks the right tool for the right intent.
func toolImageExtract(args map[string]any) ToolResult {
	res := toolOCRExtract(args)
	res.ToolName = "image_extract"
	return res
}

func runTesseract(path, lang, toolName string) ToolResult {
	// Tesseract writes to <out>.txt. Use a temp file we clean up after.
	tmp, err := os.CreateTemp("", "janus-ocr-*")
	if err != nil {
		return ToolResult{ToolName: toolName, Success: false, Error: fmt.Sprintf("tempfile: %v", err)}
	}
	tmp.Close()
	outBase := tmp.Name()
	defer os.Remove(outBase)          // base doesn't actually exist as a file
	defer os.Remove(outBase + ".txt") // but the .txt output does

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "tesseract", path, outBase, "-l", lang)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return ToolResult{
			ToolName: toolName,
			Success:  false,
			Error:    fmt.Sprintf("tesseract failed: %v. stderr: %s", err, truncateStr(stderr.String(), 300)),
		}
	}

	data, err := os.ReadFile(outBase + ".txt")
	if err != nil {
		return ToolResult{ToolName: toolName, Success: false, Error: fmt.Sprintf("read tesseract output: %v", err)}
	}

	text := strings.TrimSpace(string(data))
	if text == "" {
		return ToolResult{ToolName: toolName, Success: true, Output: "(OCR returned no text — image may be too low resolution or not contain recognizable text)"}
	}
	if len(text) > 8000 {
		text = text[:8000] + "\n\n... [truncated]"
	}
	return ToolResult{ToolName: toolName, Success: true, Output: fmt.Sprintf("[OCR lang=%s]\n%s", lang, text)}
}

// ═══════════════════════════════════════════════════════════════════════════
// docx_extract — Pure Go .docx text extraction
// ═══════════════════════════════════════════════════════════════════════════

// docxParagraph and docxText mirror the OOXML structure we care about.
// A .docx is just a zip. word/document.xml contains <w:p> paragraphs, each
// holding <w:r> runs, each holding <w:t> text nodes. We ignore formatting,
// drawings, and headers — clinicians just want the words.
type docxDocument struct {
	XMLName xml.Name `xml:"document"`
	Body    struct {
		Paragraphs []struct {
			Runs []struct {
				Texts []struct {
					Value string `xml:",chardata"`
				} `xml:"t"`
			} `xml:"r"`
		} `xml:"p"`
	} `xml:"body"`
}

func toolDOCXExtract(args map[string]any) (result ToolResult) {
	path, _ := args["path"].(string)
	if path == "" {
		return ToolResult{ToolName: "docx_extract", Success: false, Error: "path is required"}
	}
	if !filepath.IsAbs(path) {
		if abs, err := filepath.Abs(path); err == nil {
			path = abs
		}
	}
	if _, err := os.Stat(path); err != nil {
		return ToolResult{ToolName: "docx_extract", Success: false, Error: fmt.Sprintf("file not found: %s", path)}
	}

	defer func() {
		if r := recover(); r != nil {
			result = ToolResult{
				ToolName: "docx_extract",
				Success:  false,
				Error:    fmt.Sprintf("docx parser crashed: %v — file may be corrupted or password-protected", r),
			}
		}
	}()

	zr, err := zip.OpenReader(path)
	if err != nil {
		return ToolResult{ToolName: "docx_extract", Success: false, Error: fmt.Sprintf("not a valid .docx (zip) file: %v", err)}
	}
	defer zr.Close()

	// Find word/document.xml inside the zip.
	var docXML *zip.File
	for _, f := range zr.File {
		if f.Name == "word/document.xml" {
			docXML = f
			break
		}
	}
	if docXML == nil {
		return ToolResult{ToolName: "docx_extract", Success: false, Error: "word/document.xml not found — file is not a standard .docx"}
	}

	rc, err := docXML.Open()
	if err != nil {
		return ToolResult{ToolName: "docx_extract", Success: false, Error: fmt.Sprintf("open document.xml: %v", err)}
	}
	defer rc.Close()

	raw, err := io.ReadAll(rc)
	if err != nil {
		return ToolResult{ToolName: "docx_extract", Success: false, Error: fmt.Sprintf("read document.xml: %v", err)}
	}

	var doc docxDocument
	if err := xml.Unmarshal(raw, &doc); err != nil {
		return ToolResult{ToolName: "docx_extract", Success: false, Error: fmt.Sprintf("parse xml: %v", err)}
	}

	// Concatenate paragraphs with newlines, runs with nothing.
	var sb strings.Builder
	for _, p := range doc.Body.Paragraphs {
		for _, r := range p.Runs {
			for _, t := range r.Texts {
				sb.WriteString(t.Value)
			}
		}
		sb.WriteString("\n")
	}

	text := strings.TrimSpace(sb.String())
	if text == "" {
		return ToolResult{ToolName: "docx_extract", Success: true, Output: "(docx contained no text — possibly only images or tables)"}
	}
	if len(text) > 8000 {
		text = text[:8000] + "\n\n... [truncated]"
	}
	return ToolResult{ToolName: "docx_extract", Success: true, Output: text}
}

// ═══════════════════════════════════════════════════════════════════════════
// render_pdf — Generate a PDF from text/markdown
// ═══════════════════════════════════════════════════════════════════════════

// Minimal Markdown handling: we recognize #/##/### headings and bullet lists.
// Anything fancier is treated as plain text. This is intentional — the AI
// owns formatting, we own rendering.
var (
	headingRE = regexp.MustCompile(`^(#{1,3})\s+(.+)$`)
	bulletRE  = regexp.MustCompile(`^[-*]\s+(.+)$`)
)

func toolRenderPDF(args map[string]any) ToolResult {
	content, _ := args["content"].(string)
	if content == "" {
		return ToolResult{ToolName: "render_pdf", Success: false, Error: "content is required"}
	}
	title, _ := args["title"].(string)
	baseName, _ := args["filename"].(string)
	if baseName == "" {
		baseName = "document"
	}
	baseName = sanitizeOutputName(baseName)

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return ToolResult{ToolName: "render_pdf", Success: false, Error: fmt.Sprintf("mkdir: %v", err)}
	}

	pdfDoc := fpdf.New("P", "mm", "Letter", "")
	pdfDoc.SetMargins(20, 20, 20)
	pdfDoc.SetAutoPageBreak(true, 20)
	pdfDoc.AddPage()

	if title != "" {
		pdfDoc.SetFont("Helvetica", "B", 18)
		pdfDoc.MultiCell(0, 9, title, "", "L", false)
		pdfDoc.Ln(2)
		pdfDoc.SetDrawColor(180, 180, 180)
		pdfDoc.Line(20, pdfDoc.GetY(), 195, pdfDoc.GetY())
		pdfDoc.Ln(5)
	}

	pdfDoc.SetFont("Helvetica", "", 11)
	for _, rawLine := range strings.Split(content, "\n") {
		line := strings.TrimRight(rawLine, "\r")
		if strings.TrimSpace(line) == "" {
			pdfDoc.Ln(3)
			continue
		}
		if m := headingRE.FindStringSubmatch(line); m != nil {
			level := len(m[1])
			size := map[int]float64{1: 15, 2: 13, 3: 12}[level]
			pdfDoc.Ln(2)
			pdfDoc.SetFont("Helvetica", "B", size)
			pdfDoc.MultiCell(0, 7, m[2], "", "L", false)
			pdfDoc.SetFont("Helvetica", "", 11)
			continue
		}
		if m := bulletRE.FindStringSubmatch(line); m != nil {
			pdfDoc.MultiCell(0, 6, "  • "+m[1], "", "L", false)
			continue
		}
		pdfDoc.MultiCell(0, 6, line, "", "L", false)
	}

	stamp := time.Now().UTC().Format("20060102-150405")
	outPath := filepath.Join(outputDir, fmt.Sprintf("%s-%s.pdf", stamp, baseName))
	if err := pdfDoc.OutputFileAndClose(outPath); err != nil {
		return ToolResult{ToolName: "render_pdf", Success: false, Error: fmt.Sprintf("write pdf: %v", err)}
	}
	return ToolResult{
		ToolName: "render_pdf",
		Success:  true,
		Output:   fmt.Sprintf("PDF created at %s", filepath.ToSlash(outPath)),
	}
}

// ═══════════════════════════════════════════════════════════════════════════
// render_docx — Generate a minimal .docx from text
// ═══════════════════════════════════════════════════════════════════════════

// A valid .docx is a zip with three required entries:
//   [Content_Types].xml
//   _rels/.rels
//   word/document.xml
// We ship the minimum skeleton needed to make Word / LibreOffice happy.

const docxContentTypes = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
<Default Extension="xml" ContentType="application/xml"/>
<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>
</Types>`

const docxRels = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>
</Relationships>`

func toolRenderDOCX(args map[string]any) ToolResult {
	content, _ := args["content"].(string)
	if content == "" {
		return ToolResult{ToolName: "render_docx", Success: false, Error: "content is required"}
	}
	title, _ := args["title"].(string)
	baseName, _ := args["filename"].(string)
	if baseName == "" {
		baseName = "document"
	}
	baseName = sanitizeOutputName(baseName)

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return ToolResult{ToolName: "render_docx", Success: false, Error: fmt.Sprintf("mkdir: %v", err)}
	}

	// Build word/document.xml by turning each input line into a <w:p>.
	// Headings (#, ##, ###) become bold paragraphs at larger sizes.
	var body strings.Builder
	body.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	body.WriteString(`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>`)

	if title != "" {
		body.WriteString(docxParagraph(title, 32, true))
	}
	for _, rawLine := range strings.Split(content, "\n") {
		line := strings.TrimRight(rawLine, "\r")
		if strings.TrimSpace(line) == "" {
			body.WriteString(`<w:p/>`)
			continue
		}
		if m := headingRE.FindStringSubmatch(line); m != nil {
			size := map[int]int{1: 30, 2: 26, 3: 22}[len(m[1])]
			body.WriteString(docxParagraph(m[2], size, true))
			continue
		}
		if m := bulletRE.FindStringSubmatch(line); m != nil {
			body.WriteString(docxParagraph("\u2022 "+m[1], 22, false))
			continue
		}
		body.WriteString(docxParagraph(line, 22, false))
	}
	// sectPr is required at end of body for Word to be happy
	body.WriteString(`<w:sectPr><w:pgSz w:w="12240" w:h="15840"/><w:pgMar w:top="1440" w:right="1440" w:bottom="1440" w:left="1440"/></w:sectPr>`)
	body.WriteString(`</w:body></w:document>`)

	stamp := time.Now().UTC().Format("20060102-150405")
	outPath := filepath.Join(outputDir, fmt.Sprintf("%s-%s.docx", stamp, baseName))

	if err := writeDocxZip(outPath, body.String()); err != nil {
		return ToolResult{ToolName: "render_docx", Success: false, Error: fmt.Sprintf("write docx: %v", err)}
	}
	return ToolResult{
		ToolName: "render_docx",
		Success:  true,
		Output:   fmt.Sprintf("DOCX created at %s", filepath.ToSlash(outPath)),
	}
}

// docxParagraph wraps text in the minimum OOXML needed for a visible paragraph.
// size is in half-points (22 = 11pt, 32 = 16pt). bold adds <w:b/>.
func docxParagraph(text string, size int, bold bool) string {
	var props strings.Builder
	if bold {
		props.WriteString(`<w:b/>`)
	}
	props.WriteString(fmt.Sprintf(`<w:sz w:val="%d"/>`, size))
	props.WriteString(fmt.Sprintf(`<w:szCs w:val="%d"/>`, size))
	escaped := xmlEscape(text)
	return fmt.Sprintf(
		`<w:p><w:r><w:rPr>%s</w:rPr><w:t xml:space="preserve">%s</w:t></w:r></w:p>`,
		props.String(), escaped,
	)
}

func writeDocxZip(outPath, documentXML string) error {
	f, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	defer zw.Close()

	entries := []struct{ name, body string }{
		{"[Content_Types].xml", docxContentTypes},
		{"_rels/.rels", docxRels},
		{"word/document.xml", documentXML},
	}
	for _, e := range entries {
		w, err := zw.Create(e.name)
		if err != nil {
			return err
		}
		if _, err := w.Write([]byte(e.body)); err != nil {
			return err
		}
	}
	return nil
}

// ═══════════════════════════════════════════════════════════════════════════
// helpers
// ═══════════════════════════════════════════════════════════════════════════

func sanitizeOutputName(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r == '/' || r == '\\' || r == ':' || r == '*' || r == '?' ||
			r == '"' || r == '<' || r == '>' || r == '|':
			b.WriteRune('_')
		case r < 32 || r == 127:
			// skip control characters
		default:
			b.WriteRune(r)
		}
	}
	out := strings.TrimSpace(b.String())
	if out == "" || out == "." || out == ".." {
		return "document"
	}
	return out
}

func xmlEscape(s string) string {
	var b bytes.Buffer
	xml.EscapeText(&b, []byte(s))
	return b.String()
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
