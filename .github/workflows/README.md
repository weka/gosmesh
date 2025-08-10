# GitHub Workflows

## Release Workflow (`release.yml`)

Automated build and release workflow that:

### Triggers
- **Push to main branch**: Builds and potentially releases
- **Pull requests**: Builds only (no release)

### Semantic Versioning
The workflow analyzes commit messages for semantic commit patterns:
- `feat:` → Minor version bump (e.g., 1.0.0 → 1.1.0)
- `fix:` → Patch version bump (e.g., 1.0.0 → 1.0.1)
- `feat!:` or `fix!:` → Major version bump (e.g., 1.0.0 → 2.0.0)
- `chore:` or `docs:` → Patch version bump (e.g., 1.0.0 → 1.0.1)

Non-semantic commits are ignored for versioning.

### Build Process
1. **Code validation**: Runs `go vet` and tests
2. **Cross-compilation**: Builds for Linux AMD64 and ARM64
3. **Checksums**: Generates SHA256 checksums for all binaries

### Release Artifacts
- `gosmesh-linux-amd64` - Linux x86_64 binary
- `gosmesh-linux-arm64` - Linux ARM64 binary  
- `checksums.txt` - SHA256 checksums

### Usage
Just push semantic commits to main branch:
```bash
git commit -m "feat: add new network optimization feature"
git commit -m "fix: resolve connection timeout issue"
git commit -m "chore: update dependencies"
```

The workflow will automatically:
1. Analyze commits since last release
2. Determine appropriate version bump
3. Build cross-platform binaries
4. Create GitHub release with binaries attached
5. Generate changelog from commit messages