package main

import (
	"archive/zip"
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// GenerateXLSX produces a minimal valid .xlsx file from transactions.
// It uses only the standard library (archive/zip).
func GenerateXLSX(transactions []Transaction, startingBalance float64) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	// Shared strings table (all string cell values)
	headers := []string{"#", "Дата", "Тип", "Категория", "Описание", "Сумма", "Пользователь"}
	sst, sstIndex := buildSST(transactions, headers)

	// Build sheet data
	sheetXML := buildSheet(transactions, startingBalance, sstIndex, headers)

	files := map[string]string{
		"[Content_Types].xml":            contentTypesXML(),
		"_rels/.rels":                    relsXML(),
		"xl/workbook.xml":                workbookXML(),
		"xl/_rels/workbook.xml.rels":     workbookRelsXML(),
		"xl/sharedStrings.xml":           sst,
		"xl/styles.xml":                  stylesXML(),
		"xl/worksheets/sheet1.xml":       sheetXML,
		"docProps/app.xml":               appXML(),
		"docProps/core.xml":              coreXML(),
	}

	// Order matters for ZIP
	order := []string{
		"[Content_Types].xml",
		"_rels/.rels",
		"docProps/app.xml",
		"docProps/core.xml",
		"xl/workbook.xml",
		"xl/_rels/workbook.xml.rels",
		"xl/sharedStrings.xml",
		"xl/styles.xml",
		"xl/worksheets/sheet1.xml",
	}

	for _, name := range order {
		w, err := zw.Create(name)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write([]byte(files[name])); err != nil {
			return nil, err
		}
	}

	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// buildSST builds the sharedStrings.xml and returns an index mapping string → sstID.
func buildSST(transactions []Transaction, headers []string) (string, map[string]int) {
	index := make(map[string]int)
	var strs []string

	add := func(s string) {
		if _, ok := index[s]; !ok {
			index[s] = len(strs)
			strs = append(strs, s)
		}
	}

	for _, h := range headers {
		add(h)
	}
	for _, tx := range transactions {
		add(txTypeLabel(tx.Type))
		add(tx.Category)
		add(tx.Description)
		add(tx.Username)
	}
	for _, label := range []string{"Начальный баланс", "Итого приход", "Итого расход", "Текущий баланс"} {
		add(label)
	}

	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	sb.WriteString(fmt.Sprintf(`<sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" count="%d" uniqueCount="%d">`,
		len(strs), len(strs)))
	for _, s := range strs {
		sb.WriteString("<si><t xml:space=\"preserve\">")
		sb.WriteString(escapeXML(s))
		sb.WriteString("</t></si>")
	}
	sb.WriteString("</sst>")
	return sb.String(), index
}

func buildSheet(transactions []Transaction, startingBalance float64, sst map[string]int, headers []string) string {
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	sb.WriteString(`<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">`)
	sb.WriteString(`<sheetData>`)

	rowNum := 1

	// Header row (style 1 = bold)
	sb.WriteString(fmt.Sprintf(`<row r="%d">`, rowNum))
	cols := []string{"A", "B", "C", "D", "E", "F", "G"}
	for i, h := range headers {
		sb.WriteString(sstCell(cols[i], rowNum, sst[h], 1))
	}
	sb.WriteString("</row>")
	rowNum++

	// Data rows
	var totalIncome, totalExpense float64
	for _, tx := range transactions {
		if tx.Type == "income" {
			totalIncome += tx.Amount
		} else {
			totalExpense += tx.Amount
		}

		sb.WriteString(fmt.Sprintf(`<row r="%d">`, rowNum))
		sb.WriteString(numCell("A", rowNum, float64(tx.ID), 0))
		sb.WriteString(dateCell("B", rowNum, tx.Date))
		sb.WriteString(sstCell("C", rowNum, sst[txTypeLabel(tx.Type)], 0))
		sb.WriteString(sstCell("D", rowNum, sst[tx.Category], 0))
		sb.WriteString(sstCell("E", rowNum, sst[tx.Description], 0))
		sb.WriteString(numCell("F", rowNum, tx.Amount, 0))
		sb.WriteString(sstCell("G", rowNum, sst[tx.Username], 0))
		sb.WriteString("</row>")
		rowNum++
	}

	// Empty separator row
	rowNum++

	// Summary rows
	summaries := []struct {
		label  string
		amount float64
	}{
		{"Начальный баланс", startingBalance},
		{"Итого приход", totalIncome},
		{"Итого расход", totalExpense},
		{"Текущий баланс", startingBalance + totalIncome - totalExpense},
	}
	for _, s := range summaries {
		sb.WriteString(fmt.Sprintf(`<row r="%d">`, rowNum))
		sb.WriteString(sstCell("E", rowNum, sst[s.label], 1))
		sb.WriteString(numCell("F", rowNum, s.amount, 0))
		sb.WriteString("</row>")
		rowNum++
	}

	sb.WriteString("</sheetData>")
	sb.WriteString(`<cols>`)
	sb.WriteString(`<col min="1" max="1" width="5" customWidth="1"/>`)   // #
	sb.WriteString(`<col min="2" max="2" width="20" customWidth="1"/>`)  // date
	sb.WriteString(`<col min="3" max="3" width="12" customWidth="1"/>`)  // type
	sb.WriteString(`<col min="4" max="4" width="18" customWidth="1"/>`)  // category
	sb.WriteString(`<col min="5" max="5" width="35" customWidth="1"/>`)  // description
	sb.WriteString(`<col min="6" max="6" width="15" customWidth="1"/>`)  // amount
	sb.WriteString(`<col min="7" max="7" width="25" customWidth="1"/>`)  // user
	sb.WriteString(`</cols>`)
	sb.WriteString("</worksheet>")
	return sb.String()
}

// sstCell creates a shared-string cell reference.
func sstCell(col string, row, sstID, style int) string {
	ref := col + strconv.Itoa(row)
	return fmt.Sprintf(`<c r="%s" t="s" s="%d"><v>%d</v></c>`, ref, style, sstID)
}

// numCell creates a numeric cell.
func numCell(col string, row int, val float64, style int) string {
	ref := col + strconv.Itoa(row)
	return fmt.Sprintf(`<c r="%s" s="%d"><v>%s</v></c>`, ref, style, formatFloat(val))
}

// dateCell creates a cell containing a date string as shared string.
// We treat dates as plain text for simplicity.
func dateCell(col string, row int, t time.Time) string {
	ref := col + strconv.Itoa(row)
	// Inline string cell
	dateStr := escapeXML(t.Format("02.01.2006 15:04"))
	return fmt.Sprintf(`<c r="%s" t="inlineStr"><is><t>%s</t></is></c>`, ref, dateStr)
}

func txTypeLabel(t string) string {
	if t == "income" {
		return "Приход"
	}
	return "Расход"
}

func formatFloat(f float64) string {
	return strconv.FormatFloat(f, 'f', 2, 64)
}

func escapeXML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	return s
}

// ---- Static XML parts ----

func contentTypesXML() string {
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
  <Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
  <Default Extension="xml" ContentType="application/xml"/>
  <Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>
  <Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>
  <Override PartName="/xl/sharedStrings.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sharedStrings+xml"/>
  <Override PartName="/xl/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.styles+xml"/>
  <Override PartName="/docProps/app.xml" ContentType="application/vnd.openxmlformats-officedocument.extended-properties+xml"/>
  <Override PartName="/docProps/core.xml" ContentType="application/vnd.openxmlformats-package.core-properties+xml"/>
</Types>`
}

func relsXML() string {
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/>
  <Relationship Id="rId2" Type="http://schemas.openxmlformats.org/package/2006/relationships/metadata/core-properties" Target="docProps/core.xml"/>
  <Relationship Id="rId3" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/extended-properties" Target="docProps/app.xml"/>
</Relationships>`
}

func workbookXML() string {
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"
          xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">
  <sheets>
    <sheet name="Баланс СНТ" sheetId="1" r:id="rId1"/>
  </sheets>
</workbook>`
}

func workbookRelsXML() string {
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/>
  <Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/sharedStrings" Target="sharedStrings.xml"/>
  <Relationship Id="rId3" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/>
</Relationships>`
}

func stylesXML() string {
	// style 0 = normal, style 1 = bold
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<styleSheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">
  <fonts count="2">
    <font><sz val="11"/><name val="Calibri"/></font>
    <font><b/><sz val="11"/><name val="Calibri"/></font>
  </fonts>
  <fills count="2">
    <fill><patternFill patternType="none"/></fill>
    <fill><patternFill patternType="gray125"/></fill>
  </fills>
  <borders count="1">
    <border><left/><right/><top/><bottom/><diagonal/></border>
  </borders>
  <cellStyleXfs count="1">
    <xf numFmtId="0" fontId="0" fillId="0" borderId="0"/>
  </cellStyleXfs>
  <cellXfs count="2">
    <xf numFmtId="0" fontId="0" fillId="0" borderId="0" xfId="0"/>
    <xf numFmtId="0" fontId="1" fillId="0" borderId="0" xfId="0"/>
  </cellXfs>
</styleSheet>`
}

func appXML() string {
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Properties xmlns="http://schemas.openxmlformats.org/officeDocument/2006/extended-properties">
  <Application>SNT Bot</Application>
</Properties>`
}

func coreXML() string {
	now := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties"
                   xmlns:dc="http://purl.org/dc/elements/1.1/"
                   xmlns:dcterms="http://purl.org/dc/terms/"
                   xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">
  <dc:creator>SNT Bot</dc:creator>
  <dcterms:created xsi:type="dcterms:W3CDTF">%s</dcterms:created>
</cp:coreProperties>`, now)
}
