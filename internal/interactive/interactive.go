// Package interactive implements the console question/answer REPL that
// starts when aiac9 is launched without arguments.
package interactive

import (
	"bufio"
	"fmt"
	"io"
	"math/rand"
	"strings"
)

// yesProbability is the chance (0..1) that an answer is "да".
const yesProbability = 0.77

// Run starts the interactive question/answer loop: it prompts on out, reads
// a line from in for each question, and replies with a randomly chosen
// answer — "да" with 77% probability, "нет" with 23%. The loop ends when the
// user types "exit"/"quit" or in is exhausted (EOF, e.g. Ctrl+D).
func Run(in io.Reader, out io.Writer) error {
	fmt.Fprintln(out, "aiac9 — интерактивный режим. Введите вопрос (или \"exit\" для выхода).")

	scanner := bufio.NewScanner(in)
	for {
		fmt.Fprint(out, "> ")
		if !scanner.Scan() {
			fmt.Fprintln(out)
			return scanner.Err()
		}

		question := strings.TrimSpace(scanner.Text())
		switch {
		case question == "":
			continue
		case strings.EqualFold(question, "exit"), strings.EqualFold(question, "quit"):
			return nil
		}

		fmt.Fprintln(out, answer())
	}
}

// answer returns "да" with yesProbability, "нет" otherwise.
func answer() string {
	if rand.Float64() < yesProbability {
		return "да"
	}
	return "нет"
}
