#!/bin/sh
# Makes a macOS install tree relocatable and checks it is self-contained.
# Usage: relocate-macho.sh ROOT [OLD_PREFIX]
# Library references under OLD_PREFIX become @rpath/NAME, every Mach-O file
# gets rpaths to lib/ next to it, and the result must only depend on the
# system or on files inside ROOT. Files are re-signed ad hoc afterwards,
# because Apple Silicon refuses to run modified unsigned code.
set -eu

root="$(cd "$1" && pwd)"
prefix="${2:-}"
status=0

machos="$(find "$root" -type f \( -perm -u+x -o -name '*.dylib' -o -name '*.so' \) -exec sh -c 'file -b "$1" | grep -q Mach-O && echo "$1"' _ {} \;)"

for file in $machos; do
	chmod u+w "$file"
	if [ -n "$prefix" ]; then
		case "$file" in
		*.dylib) install_name_tool -id "@rpath/$(basename "$file")" "$file" 2>/dev/null || true ;;
		esac
		otool -L "$file" | tail -n +2 | awk '{print $1}' | while read -r dep; do
			case "$dep" in
			"$prefix"/*) install_name_tool -change "$dep" "@rpath/$(basename "$dep")" "$file" ;;
			esac
		done
	fi
	# lib/ may sit beside, one level up, or two levels up (plugins in lib/x/).
	for rpath in @loader_path @loader_path/../lib @loader_path/.. @loader_path/../..; do
		install_name_tool -add_rpath "$rpath" "$file" 2>/dev/null || true
	done
	codesign --force --sign - "$file" 2>/dev/null
done

for file in $machos; do
	otool -L "$file" | tail -n +2 | awk '{print $1}' | while read -r dep; do
		case "$dep" in
		/usr/lib/* | /System/* | @rpath/* | @loader_path/* | @executable_path/*) ;;
		*) echo "not self-contained: $file -> $dep" ;;
		esac
	done
done | tee /tmp/relocate-problems.txt
if [ -s /tmp/relocate-problems.txt ]; then
	status=1
fi
exit "$status"
