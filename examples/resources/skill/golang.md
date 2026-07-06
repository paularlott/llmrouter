# Go Programming Skill

## Conventions

- Always handle errors explicitly — never use `_ =` to discard errors
- Use `context.Context` as the first parameter on all functions that do I/O
- Prefer composition over inheritance — embed structs, don't extend them
- Interface names should end with `-er` (Reader, Writer, Closer)
- Return concrete types, accept interfaces

## Testing

- Table-driven tests: `func TestFoo(t *testing.T) { tests := []struct{...}{...}; for _, tt := range tests { t.Run(tt.name, func(t *testing.T) {...}) } }`
- Use `t.Parallel()` for independent tests
- Test files: `foo_test.go` in the same package

## Error Handling

- Wrap errors with context: `fmt.Errorf("doing X: %w", err)`
- Check errors with `errors.Is` and `errors.As`, not `==`
- Sentinel errors: define as `var ErrNotFound = errors.New("not found")`
