#!/usr/bin/env bash
# proc-info.sh — portable process introspection, sourced by scripts/kandev-*.
#
# Linux reads /proc; macOS has no /proc, so the same facts come from `ps` and
# `lsof`. Every function prints its value (or KEY=VALUE lines) and returns 0
# with empty output when the fact is unavailable, so callers keep `set -e`
# without probing for the platform themselves.
#
# Sourced, not executed:
#   . "$(dirname "${BASH_SOURCE[0]}")/lib/proc-info.sh"
#
# Deliberately bash-3.2 compatible: macOS ships bash 3.2 as /bin/bash, and a
# `#!/usr/bin/env bash` script inherits it whenever Homebrew's bash is not
# first on PATH. No `declare -A`, no `mapfile`, no `${var,,}`.

# Print a process's environment as KEY=VALUE lines (empty when unreadable).
proc_environ() {
  local pid="$1"
  if [ -r "/proc/$pid/environ" ]; then
    tr '\0' '\n' <"/proc/$pid/environ" 2>/dev/null || true
    return 0
  fi
  # macOS/BSD: `ps eww` appends the environment to the command line, so the
  # tokens after the command are KEY=VALUE pairs. A value containing spaces
  # splits into extra tokens; every key read from here is a path, a port, or a
  # manager name, so a split cannot fuse two keys into one.
  ps eww -p "$pid" 2>/dev/null | tr ' ' '\n' || true
}

# Print a process's command line, arguments space-joined (empty when gone).
proc_cmdline() {
  local pid="$1"
  if [ -r "/proc/$pid/cmdline" ]; then
    tr '\0' ' ' <"/proc/$pid/cmdline" 2>/dev/null || true
    return 0
  fi
  ps -o command= -p "$pid" 2>/dev/null || true
}

# Print the executable path of a process (empty when unavailable).
proc_exe() {
  local pid="$1" exe
  if [ -L "/proc/$pid/exe" ]; then
    readlink "/proc/$pid/exe" 2>/dev/null || true
    return 0
  fi
  # macOS: `ps -o comm=` reports the executable path for processes we own,
  # which is what the /proc/pid/exe callers expect. Fall back to lsof's text
  # segment when comm carries only a bare name.
  exe="$(ps -o comm= -p "$pid" 2>/dev/null || true)"
  case "$exe" in
    /*) printf '%s\n' "$exe"; return 0 ;;
  esac
  lsof -p "$pid" -a -d txt -Fn 2>/dev/null | sed -n 's/^n//p' | head -1 || true
}

# Print the working directory of a process (empty when unavailable).
proc_cwd() {
  local pid="$1"
  if [ -L "/proc/$pid/cwd" ]; then
    readlink "/proc/$pid/cwd" 2>/dev/null || true
    return 0
  fi
  lsof -p "$pid" -a -d cwd -Fn 2>/dev/null | sed -n 's/^n//p' | head -1 || true
}

# Print "PPID PID" pairs of a process's children, one per line.
proc_children() {
  local pid="$1"
  pgrep -P "$pid" 2>/dev/null || true
}

# True when the PID exists and is signalable by us.
proc_exists() {
  kill -0 "$1" 2>/dev/null
}

# True when the PID exists and has not already exited. A zombie (state Z) has
# finished; it lingers only until its parent reaps it, so callers waiting on a
# kill must treat it as gone.
proc_alive() {
  local pid="$1" stat
  proc_exists "$pid" || return 1
  stat="$(ps -o stat= -p "$pid" 2>/dev/null | tr -d '[:space:]')"
  [ -n "$stat" ] || return 1
  case "$stat" in
    Z*) return 1 ;;
  esac
  return 0
}
