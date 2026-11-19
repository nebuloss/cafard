package blob

// DetectImage is the type of the image whose first bytes (512 are enough)
// are head, by content as Put sniffs it, or "" when it is not an image
// cafard accepts. Clients use it to refuse a file before sending it.
func DetectImage(head []byte) string {
	if m := sniff(head); IsImage(m) {
		return m
	}
	return ""
}
