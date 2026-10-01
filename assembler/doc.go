// Package assembler builds bytecode layouts with short-to-long jump relaxation.
//
// Assembler is safe for concurrent use. Assembly methods operate on a copied
// Snapshot, so repeated assembly is pure and a failed assembly cannot leave a
// partial layout behind.
package assembler
