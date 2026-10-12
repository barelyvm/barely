BARELY_HOME ?= $(HOME)/.barely
.PHONY: kernel kernel-config kernel-clean libkrun barely barelyvm agent guest man

OUT := out/bin
LIBCLANG := $(shell xcode-select -p)/Toolchains/XcodeDefault.xctoolchain/usr/lib
CARGO_ENV := PATH=/opt/homebrew/opt/rustup/bin:/opt/homebrew/opt/lld/bin:$$PATH LIBCLANG_PATH=$(LIBCLANG)

# Guest image.
# The second line freezes the timestamp: `?=` alone is still a recursive
# variable, so every $(GUEST_VERSION) reference below would re-run `date`
# and the guest target and timestamp would drift apart mid-build.
GUEST_VERSION ?= $(shell date -u +%Y.%m.%d.%H%M)
GUEST_VERSION := $(GUEST_VERSION)
GUEST_BUILD_IMAGE := barely-guest-build
GUEST_OUT := out/guest/$(GUEST_VERSION)
KERNEL_VERSION := $(shell . kernel/version.env && echo $$KERNEL_VERSION)
AGENT_PROTOCOL := 3
FSEVENT_ABI := 1

kernel kernel-config kernel-clean:
	$(MAKE) -C kernel $@

# Builds barely's libkrun fork and ships the dylib next to the barely binary,
# found at runtime via rpath, plus the header internal/krun compiles against.
# The fork is fetched into out/libkrun at the commit libkrun.env pins. Set
# LIBKRUN_DIR to build a working copy instead: whatever is checked out there
# is built, so the last line says which commit that was.
include libkrun.env
LIBKRUN_DIR ?= out/libkrun
libkrun:
ifeq ($(LIBKRUN_DIR),out/libkrun)
	test -d out/libkrun/.git || git clone -q $(LIBKRUN_URL) out/libkrun
	git -C out/libkrun cat-file -e '$(LIBKRUN_COMMIT)^{commit}' 2>/dev/null || git -C out/libkrun fetch -q $(LIBKRUN_URL) $(LIBKRUN_COMMIT)
	git -C out/libkrun checkout -q --detach $(LIBKRUN_COMMIT)
endif
	cd $(LIBKRUN_DIR) && $(CARGO_ENV) $(MAKE) BLK=1 NET=1
	mkdir -p $(OUT) out/include
	cp $(LIBKRUN_DIR)/include/libkrun.h out/include/libkrun.h
	cp $(LIBKRUN_DIR)/target/release/libkrun.dylib $(OUT)/libkrun.1.dylib
	install_name_tool -id @rpath/libkrun.1.dylib $(OUT)/libkrun.1.dylib
	codesign -s - -f $(OUT)/libkrun.1.dylib
	@echo "libkrun: built $$(git -C $(LIBKRUN_DIR) rev-parse HEAD) from $(LIBKRUN_DIR); libkrun.env pins $(LIBKRUN_COMMIT)"

# Builds the barely binary (host process, VM process and CLI).
# barely needs no signing: only barelyvm needs the hypervisor entitlement.
barely:
	mkdir -p $(OUT)
	go build -o $(OUT)/barely ./cmd/barely

# barelyvm runs the VM; libkrun.1.dylib is found next to it (@executable_path).
barelyvm:
	mkdir -p $(OUT)
	go build -ldflags '-extldflags "-Wl,-rpath,@executable_path"' -o $(OUT)/barelyvm ./cmd/barelyvm
	codesign -s - --entitlements barely.entitlements --force $(OUT)/barelyvm

# Generates the barely(1) man page from the help text in cmd/barely.
man: barely
	mkdir -p out/man
	$(OUT)/barely man -o out/man/barely.1

# Builds barely-agent for the guest (linux/arm64, no cgo: it only uses vsock and
# the mount/network syscalls, both pure Go). AGENT_PROTOCOL must match
# internal/proto.Version.
agent:
	mkdir -p out/guest-build/agent
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build \
		-ldflags "-X main.version=$(GUEST_VERSION)" \
		-o out/guest-build/agent/barely-agent ./cmd/barely-agent

# Builds the guest image (kernel, Alpine root.img, initrd.img carrying
# barely-agent as /init, manifest.json) and installs it to ~/.barely/guest/.
guest: kernel agent
	docker build --platform linux/arm64 -q -t $(GUEST_BUILD_IMAGE) guest
	mkdir -p $(GUEST_OUT)
	cp kernel/out/Image $(GUEST_OUT)/Image
	docker run --rm --platform linux/arm64 \
		-v $(CURDIR)/guest:/guest:ro \
		-v $(CURDIR)/out/guest-build:/build \
		-v $(CURDIR)/$(GUEST_OUT):/out \
		$(GUEST_BUILD_IMAGE) sh /guest/build.sh "$(GUEST_VERSION)" "$(KERNEL_VERSION)" "$(AGENT_PROTOCOL)" "$(FSEVENT_ABI)"
	mkdir -p $(BARELY_HOME)/guest
	rm -rf $(BARELY_HOME)/guest/$(GUEST_VERSION)
	cp -R $(GUEST_OUT) $(BARELY_HOME)/guest/$(GUEST_VERSION)
	@echo "guest $(GUEST_VERSION) installed to $(BARELY_HOME)/guest/$(GUEST_VERSION)"
