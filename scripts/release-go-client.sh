#!/bin/bash
set -e

# Release script for go.tracewayapp.com Go client library (multi-module)
# Releases core module first, then all sub-modules with the same version.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Known sub-modules
SUBMODULES=("tracewaygin" "tracewayhttp" "tracewaydb")

CORE_MODULE="go.tracewayapp.com"

usage() {
    echo "Usage: $0 <version>"
    echo ""
    echo "Arguments:"
    echo "  version    Semantic version (e.g., v0.4.0)"
    echo ""
    echo "Releases the core module and all sub-modules with the given version."
    echo ""
    echo "Examples:"
    echo "  $0 v0.4.0"
    exit 1
}

# --- Step 1: Pre-flight checks ---

if [ -z "$1" ]; then
    echo -e "${RED}Error: Version argument required${NC}"
    usage
fi

VERSION="$1"

# Validate semver format (vX.Y.Z or vX.Y.Z-suffix)
if ! [[ "$VERSION" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[a-zA-Z0-9.]+)?$ ]]; then
    echo -e "${RED}Error: Invalid version format '$VERSION'${NC}"
    echo "Version must be in semver format: vX.Y.Z (e.g., v1.0.0)"
    exit 1
fi

cd "$PROJECT_ROOT"

echo -e "${GREEN}=== Pre-flight checks ===${NC}"

# Check for uncommitted changes
if [ -n "$(git status --porcelain)" ]; then
    echo -e "${RED}Error: You have uncommitted changes${NC}"
    echo "Please commit all changes before releasing."
    git status --short
    exit 1
fi
echo -e "${GREEN}No uncommitted changes${NC}"

# Build and vet core module
echo -e "${YELLOW}Building and vetting core module...${NC}"
(cd "$PROJECT_ROOT" && go build ./... && go vet ./...)
echo -e "${GREEN}Core module OK${NC}"

# Build and vet each sub-module
for mod in "${SUBMODULES[@]}"; do
    echo -e "${YELLOW}Building and vetting $mod...${NC}"
    (cd "$PROJECT_ROOT/$mod" && go build ./... && go vet ./...)
    echo -e "${GREEN}$mod OK${NC}"
done

echo ""

# --- Step 2: Release the core (parent) module ---

echo -e "${GREEN}=== Releasing core module: $CORE_MODULE $VERSION ===${NC}"

git tag -a "$VERSION" -m "Release $CORE_MODULE $VERSION"
echo -e "${GREEN}Created tag: $VERSION${NC}"

git push origin "$VERSION"
echo -e "${GREEN}Pushed tag: $VERSION${NC}"
echo ""

# --- Step 3: Prepare sub-modules for release ---

echo -e "${GREEN}=== Preparing sub-modules for release ===${NC}"

for mod in "${SUBMODULES[@]}"; do
    MOD_DIR="$PROJECT_ROOT/$mod"
    MOD_FILE="$MOD_DIR/go.mod"

    echo -e "${YELLOW}Updating $mod/go.mod...${NC}"

    # Remove the replace directive
    sed -i '' '/^replace go\.tracewayapp\.com => \.\.\/$/d' "$MOD_FILE"

    # Update the require version of go.tracewayapp.com to the new version
    sed -i '' "s|go\.tracewayapp\.com v[^ ]*|go.tracewayapp.com $VERSION|" "$MOD_FILE"

    # Run go mod tidy
    (cd "$MOD_DIR" && go mod tidy)

    echo -e "${GREEN}$mod/go.mod updated${NC}"
done

echo ""

# --- Step 4: Commit the sub-module go.mod changes ---

echo -e "${GREEN}=== Committing sub-module changes ===${NC}"

cd "$PROJECT_ROOT"
for mod in "${SUBMODULES[@]}"; do
    git add "$mod/go.mod" "$mod/go.sum"
done
git commit -m "release: prepare sub-modules for $VERSION"

echo -e "${GREEN}Committed sub-module changes${NC}"
echo ""

# --- Step 5: Tag and push sub-modules ---

echo -e "${GREEN}=== Tagging sub-modules ===${NC}"

for mod in "${SUBMODULES[@]}"; do
    TAG="${mod}/${VERSION}"
    git tag -a "$TAG" -m "Release $CORE_MODULE/$mod $VERSION"
    echo -e "${GREEN}Created tag: $TAG${NC}"
done

# Push all sub-module tags
for mod in "${SUBMODULES[@]}"; do
    TAG="${mod}/${VERSION}"
    git push origin "$TAG"
    echo -e "${GREEN}Pushed tag: $TAG${NC}"
done

echo ""

# --- Step 6: Restore local development setup ---

echo -e "${GREEN}=== Restoring local replace directives ===${NC}"

for mod in "${SUBMODULES[@]}"; do
    MOD_DIR="$PROJECT_ROOT/$mod"
    MOD_FILE="$MOD_DIR/go.mod"

    echo -e "${YELLOW}Restoring $mod/go.mod...${NC}"

    # Re-add the replace directive at the end of go.mod
    echo "" >> "$MOD_FILE"
    echo "replace go.tracewayapp.com => ../" >> "$MOD_FILE"

    # Run go mod tidy
    (cd "$MOD_DIR" && go mod tidy)

    echo -e "${GREEN}$mod/go.mod restored${NC}"
done

echo ""

# --- Step 7: Commit the restoration ---

echo -e "${GREEN}=== Committing restored replace directives ===${NC}"

cd "$PROJECT_ROOT"
for mod in "${SUBMODULES[@]}"; do
    git add "$mod/go.mod" "$mod/go.sum"
done
git commit -m "chore: restore local replace directives after $VERSION release"

git push origin
echo -e "${GREEN}Pushed commits to origin${NC}"
echo ""

# --- Step 8: Summary ---

echo -e "${GREEN}=== Release Complete ===${NC}"
echo ""
echo "Tags created:"
echo "  $VERSION"
for mod in "${SUBMODULES[@]}"; do
    echo "  ${mod}/${VERSION}"
done
echo ""
echo "Install commands:"
echo "  go get $CORE_MODULE@$VERSION"
for mod in "${SUBMODULES[@]}"; do
    echo "  go get $CORE_MODULE/$mod@$VERSION"
done
