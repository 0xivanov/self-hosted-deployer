package cli

import (
	"encoding/json"
	"fmt"
	"io"
)

const (
	OutputHuman = "human"
	OutputJSON  = "json"
)

func ValidateOutputFormat(output string) error {
	switch output {
	case OutputHuman, OutputJSON:
		return nil
	default:
		return fmt.Errorf("unsupported output format %q; expected human or json", output)
	}
}

func RenderJSON(w io.Writer, value any) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func RenderFields(w io.Writer, fields ...Field) {
	for _, field := range fields {
		fmt.Fprintf(w, "%-12s %v\n", field.Name+":", field.Value)
	}
}

type Field struct {
	Name  string
	Value any
}
