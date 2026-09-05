package interactive

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/fedorvasiliev/aiac9/internal/term"
)

var spinnerFrames = []rune{'|', '/', '-', '\\'}

// runWithSpinner runs fn, animating a spinner on out for as long as it
// takes — per CLAUDE.md, "пока модель обрабатывает результат". On a real
// terminal it redraws a frame every 100ms and clears the line once fn
// returns; otherwise (piped output, tests) it just runs fn, since an
// animation makes no sense outside a terminal.
func runWithSpinner(out io.Writer, fn func()) {
	f, ok := out.(*os.File)
	if !ok || !term.IsTerminal(f.Fd()) {
		fn()
		return
	}

	stop := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			case <-ticker.C:
				fmt.Fprintf(out, "\r%c", spinnerFrames[i%len(spinnerFrames)])
			}
		}
	}()

	fn()

	// Wait for the spinner goroutine to stop before writing the clear
	// sequence ourselves, so the two never write to out concurrently.
	close(stop)
	<-stopped
	fmt.Fprint(out, "\r \r")
}
