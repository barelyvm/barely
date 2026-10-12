package main

import "syscall"

// sigWinch is the terminal-resize signal watchResize listens for. It lives in
// a _darwin file because barely's CLI only targets macOS.
const sigWinch = syscall.SIGWINCH
