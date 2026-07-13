// Package all blank-imports every DocumentExtractor plugin package so
// that a single import of this package wires up the full set of
// supported formats via each plugin's self-registering init().
//
// This file is intentionally maintained by the integrator merging each
// format plugin branch: as the docx/pptx/xlsx plugins land, add a blank
// import for each here. For now only the plain-text plugin exists.
package all

import (
	_ "officegrep/internal/adapters/extract/pptx"
	_ "officegrep/internal/adapters/extract/text"
	// The following office-format plugins are implemented on separate
	// branches by other agents and will be blank-imported here once
	// merged:
	//   _ "officegrep/internal/adapters/extract/docx"
	//   _ "officegrep/internal/adapters/extract/xlsx"
)
