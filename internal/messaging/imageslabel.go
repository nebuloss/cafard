package messaging

import "fmt"

// ImagesLabel stands for the text of a message that has only images
// ("[2 images]"), so its chat row does not read "agent: " and nothing.
// render/format.ts imagesLabel matches it.
func ImagesLabel(n int) string {
	switch n {
	case 0:
		return ""
	case 1:
		return "[1 image]"
	}
	return fmt.Sprintf("[%d images]", n)
}
