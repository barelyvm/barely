// barely-amd64-unavailable: registered as the x86-64 binfmt_misc handler, so
// an amd64 program fails with a clear message instead of "exec format error".
#include <stdio.h>

int main(void) {
	fputs("barely does not run amd64 (x86-64) programs; use an arm64 image\n", stderr);
	return 126;
}
