#!/bin/bash
# The deb's postinst: applied by dpkg at install time.
#
# Chromium refuses to start its renderer sandbox unless chrome-sandbox is
# owned by root and setuid (4755). dpkg unpacks files with ordinary
# permissions, so without this every install ends with the manual ritual:
#
#   sudo chown root:root /opt/paladin/chrome-sandbox
#   sudo chmod 4755 /opt/paladin/chrome-sandbox
#
# The path follows electron-builder's install layout: the "linux" section of
# electron-builder.json sets no prefix, so the deb installs into /opt/paladin.
# Note for editors of this file: electron-builder runs it through a macro
# templater keyed on dollar-brace tokens, so shell expansions must stay
# brace-free (dollar-VAR, never dollar-brace-VAR) or the build fails with
# "Macro ... is not defined".
# Nothing here is fatal: if the file is missing or the permissions cannot be
# applied, the install still succeeds.

set -e

SANDBOX=/opt/paladin/chrome-sandbox

if [ -f "$SANDBOX" ]; then
  chown root:root "$SANDBOX" || true
  chmod 4755 "$SANDBOX" || true
fi

exit 0
