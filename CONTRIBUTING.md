# Contributing to GoBlob

## Branch Naming

- Feature branches: `feature/<name>`
- Bug fix branches: `fix/<name>`
- Refactoring branches: `refactor/<name>`

## Commit Format

Commits should follow the format: `<type>: <description>`

Types:
- `feat` - New feature
- `fix` - Bug fix
- `test` - Adding or updating tests
- `refactor` - Code refactoring
- `docs` - Documentation changes

Examples:
- `feat: add volume balancing algorithm`
- `fix: handle race condition in volume cache`
- `test: add integration tests for S3 gateway`

## Development Workflow

1. Create a feature branch from `main`
2. Make your changes with clear commit messages
3. Run tests: `make test`
4. Run linter: `make lint`
5. Push and create a pull request

## Running Tests

```bash
# Run all tests
make test

# Run tests with coverage
go test ./goblob/... -race -coverprofile=coverage.out
```

## Building

```bash
# Build the blob binary
make build

# Clean build artifacts
make clean
```
