package term

// Style says how output may be decorated on the stream it is written to.
type Style struct {
	Unicode bool
	Color   bool
}

// Mark returns s as printed in this style: symbol or word, coloured or plain.
func (st Style) Mark(s Symbol) string {
	return Paint(Mark(s, st.Unicode), s, st.Color)
}
