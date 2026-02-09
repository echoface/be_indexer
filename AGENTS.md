# Agent Guidelines for be_indexer

## Build/Test Commands

```bash
# Run all tests
go test ./...

# Run tests with verbose output
go test -v ./...

# Run a single test function
go test -run TestBEIndex_Retrieve ./...

# Run tests in a specific package
go test ./roaringidx/...

# Run tests with coverage
go test -cover ./...

# Build the module
go build ./...

# Download dependencies
go mod download

# Tidy dependencies
go mod tidy

# Vet (static analysis)
go vet ./...
```

## Code Style Guidelines

### Imports
- Group imports: stdlib first, blank line, then project imports
- Use full import paths: `github.com/echoface/be_indexer/parser`
- Example:
```go
import (
    "fmt"
    "strings"

    "github.com/echoface/be_indexer/parser"
    "github.com/echoface/be_indexer/util"
)
```

### Types & Naming
- Use type blocks: `type ( ... )` for related type definitions
- Exported types use PascalCase, unexported use camelCase
- Use meaningful names: `IndexerBuilder`, `Document`, `Conjunction`
- Interface names end with er (e.g., `BEIndex` is acceptable as domain term)

### Error Handling
- Return errors explicitly for recoverable errors
- Use `util.PanicIf()` and `util.PanicIfErr()` for unrecoverable conditions
- Wrap errors with context: `fmt.Errorf("indexing field:%s fail:%v", field, err)`
- Behavior for bad conjunctions: configurable via `BadConjBehavior` (Error, Skip, Panic)

### Code Structure
- Comments can be in Chinese for domain-specific concepts
- Constants grouped in const blocks with iota when applicable
- Option pattern for configuration: `func WithXXX() BuilderOpt`
- Constructor functions named `NewXXX()`

### Testing
- Use GoConvey for BDD-style tests: `convey.Convey()`, `convey.So()`
- Test files named `*_test.go`
- Tests organized with nested convey blocks
- Example:
```go
func TestFeature(t *testing.T) {
    convey.Convey("test scenario", t, func() {
        convey.So(result, convey.ShouldEqual, expected)
    })
}
```

### Documentation
- Document exported functions and types
- Keep CHANGELOG.md updated for major changes
- Example code in examples/ directory

### Performance
- Document ID limit: [-2^43, 2^43] for be_indexer, [-2^56, 2^56] for roaringidx
- Use sync.Pool for frequently allocated objects when appropriate
