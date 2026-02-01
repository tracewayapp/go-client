#!/bin/bash
set -e

# Release script for go.tracewayapp.com Go client library (multi-module)

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"
GO_CLIENT_DIR="$PROJECT_ROOT"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Known sub-modules
SUBMODULES=("tracewaygin" "tracewayhttp" "tracewaydb")

usage() {
    echo "Usage: $0 <module> <version>"
    echo ""
    echo "Arguments:"
    echo "  module     Module to release: core, tracewaygin, tracewayhttp, tracewaydb"
    echo "  version    Semantic version (e.g., v1.0.0, v0.1.0)"
    echo ""
    echo "Examples:"
    echo "  $0 core v0.3.0                  # tags as v0.3.0"
    echo "  $0 tracewaygin v0.1.0           # tags as tracewaygin/v0.1.0"
    echo "  $0 tracewayhttp v0.1.0          # tags as tracewayhttp/v0.1.0"
    echo "  $0 tracewaydb v0.1.0            # tags as tracewaydb/v0.1.0"
    exit 1
}

# Check for arguments
if [ -z "$1" ] || [ -z "$2" ]; then
    echo -e "${RED}Error: Module and version arguments required${NC}"
    usage
fi

MODULE="$1"
VERSION="$2"

# Validate semver format (vX.Y.Z)
if ! [[ "$VERSION" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    echo -e "${RED}Error: Invalid version format '$VERSION'${NC}"
    echo "Version must be in semver format: vX.Y.Z (e.g., v1.0.0)"
    exit 1
fi

# Determine tag and build directory based on module
case "$MODULE" in
    core)
        TAG="$VERSION"
        BUILD_DIR="$GO_CLIENT_DIR"
        MODULE_PATH="go.tracewayapp.com"
        ;;
    tracewaygin|tracewayhttp|tracewaydb)
        TAG="${MODULE}/${VERSION}"
        BUILD_DIR="$GO_CLIENT_DIR/$MODULE"
        MODULE_PATH="go.tracewayapp.com/$MODULE"
        ;;
    *)
        echo -e "${RED}Error: Unknown module '$MODULE'${NC}"
        echo "Valid modules: core, tracewaygin, tracewayhttp, tracewaydb"
        exit 1
        ;;
esac

echo -e "${GREEN}=== Releasing $MODULE_PATH $VERSION ===${NC}"
echo -e "${YELLOW}Tag: $TAG${NC}"
echo ""

# Change to build directory
cd "$BUILD_DIR"
echo -e "${YELLOW}Working directory: $BUILD_DIR${NC}"
echo ""

# Verify the build
echo -e "${YELLOW}Building module...${NC}"
go build ./...
echo -e "${GREEN}Build successful${NC}"
echo ""

# Run vet
echo -e "${YELLOW}Running go vet...${NC}"
go vet ./...
echo -e "${GREEN}Vet passed${NC}"
echo ""

# Check for uncommitted changes
cd "$PROJECT_ROOT"
if [ -n "$(git status --porcelain)" ]; then
    echo -e "${RED}Error: You have uncommitted changes${NC}"
    echo "Please commit all changes before releasing."
    git status --short
    exit 1
fi

# Create the tag
echo -e "${YELLOW}Creating tag: $TAG${NC}"

git tag -a "$TAG" -m "Release $MODULE_PATH $VERSION"
echo -e "${GREEN}Tag created${NC}"
echo ""

# Push the tag
echo -e "${YELLOW}Pushing tag to origin...${NC}"
git push origin "$TAG"
echo -e "${GREEN}Tag pushed${NC}"
echo ""

echo -e "${GREEN}=== Release Complete ===${NC}"
echo ""
echo "Next steps:"
echo "1. The Go module proxy will automatically pick up the new version"
echo "2. Users can install with: go get $MODULE_PATH@$VERSION"
echo ""
echo "Note: Make sure go.tracewayapp.com is configured to redirect to your Git repository."
echo "See: https://go.dev/ref/mod#vcs-find for vanity import setup"
