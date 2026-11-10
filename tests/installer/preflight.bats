#!/usr/bin/env bats
# Runs install.ps1's preflight tests under PowerShell.

@test "install.ps1 judges a PC before installing" {
  command -v pwsh >/dev/null || skip "pwsh not installed"
  run pwsh -NoProfile -File "$BATS_TEST_DIRNAME/preflight.ps1"
  echo "$output"
  [ "$status" -eq 0 ]
}
