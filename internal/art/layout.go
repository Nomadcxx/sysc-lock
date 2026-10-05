package art

// MinCell is the smallest cell width, in pixels, that still reads as art.
const MinCell = 2

// CellWidth picks the cell width for rows so the clock is at most a fifth of
// the output height and 90% of its width. Cells are twice as tall as wide.
func CellWidth(rows []string, width, height int) int {
	cols := Width(rows)
	if cols == 0 || len(rows) == 0 {
		return 0
	}
	return min(height/(5*2*len(rows)), width*9/10/cols)
}

// Pick returns the first style that fits: the configured one, the default,
// then plain. plain returns no rows and a zero cell width.
func Pick(name, text string, width, height int) (Style, []string, int) {
	if name == Plain {
		return Lookup(Plain), nil, 0
	}
	for _, n := range []string{name, DefaultStyle} {
		s := Lookup(n)
		if s.Plain() {
			continue
		}
		rows := s.Compose(text)
		if cw := CellWidth(rows, width, height); cw >= MinCell {
			return s, rows, cw
		}
	}
	return Lookup(Plain), nil, 0
}
