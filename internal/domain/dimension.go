package domain

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// Dimension represents the grid footprint of a widget [cols, rows] on the 6x2 grid.
type Dimension struct {
	Cols int `yaml:"cols"`
	Rows int `yaml:"rows"`
}

// NewDimension creates a Dimension with the specified columns and rows.
func NewDimension(cols, rows int) Dimension {
	return Dimension{Cols: cols, Rows: rows}
}

// DimensionFromSlice converts a 2-element integer slice [cols, rows] to Dimension.
func DimensionFromSlice(s []int) (Dimension, error) {
	if len(s) != 2 {
		return Dimension{}, fmt.Errorf("dimension slice must contain exactly 2 integers [cols, rows], got %d", len(s))
	}
	d := Dimension{Cols: s[0], Rows: s[1]}
	if !d.IsValid() {
		return Dimension{}, fmt.Errorf("dimension [%d, %d] violates 6x2 grid bounds (cols 1..6, rows 1..2)", d.Cols, d.Rows)
	}
	return d, nil
}

// Area returns the total number of discrete cells occupied by the dimension.
func (d Dimension) Area() int {
	return d.Cols * d.Rows
}

// IsValid checks if the dimension adheres strictly to 6x2 grid bounds.
func (d Dimension) IsValid() bool {
	return d.Cols >= 1 && d.Cols <= 6 && d.Rows >= 1 && d.Rows <= 2
}

// Slice returns the dimension as a 2-element slice [cols, rows].
func (d Dimension) Slice() []int {
	return []int{d.Cols, d.Rows}
}

// String returns the string representation [cols, rows].
func (d Dimension) String() string {
	return fmt.Sprintf("[%d, %d]", d.Cols, d.Rows)
}

// UnmarshalYAML implements custom unmarshaling supporting both [cols, rows] sequences and mappings.
func (d *Dimension) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.SequenceNode:
		if len(node.Content) != 2 {
			return fmt.Errorf("line %d: dimensions sequence must contain exactly 2 integers [cols, rows], got %d", node.Line, len(node.Content))
		}
		var cols, rows int
		if err := node.Content[0].Decode(&cols); err != nil {
			return fmt.Errorf("line %d: invalid columns in dimension: %w", node.Content[0].Line, err)
		}
		if err := node.Content[1].Decode(&rows); err != nil {
			return fmt.Errorf("line %d: invalid rows in dimension: %w", node.Content[1].Line, err)
		}
		d.Cols = cols
		d.Rows = rows
		return nil
	case yaml.MappingNode:
		type rawDimension struct {
			Cols int `yaml:"cols"`
			Rows int `yaml:"rows"`
		}
		var raw rawDimension
		if err := node.Decode(&raw); err != nil {
			return err
		}
		d.Cols = raw.Cols
		d.Rows = raw.Rows
		return nil
	default:
		return fmt.Errorf("line %d: dimension must be sequence [cols, rows] or mapping", node.Line)
	}
}

// MarshalYAML serializes Dimension as a standard 2-element sequence [cols, rows].
func (d Dimension) MarshalYAML() (any, error) {
	return []int{d.Cols, d.Rows}, nil
}
