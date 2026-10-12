KERNEL_DIR := $(abspath $(dir $(lastword $(MAKEFILE_LIST))))
BUILD_IMAGE := barely-kernel-build
BUILD_VOLUME := barely-kernel-build
DOCKER_RUN := docker run --rm --platform linux/arm64 \
	-v $(KERNEL_DIR):/kernel:ro \
	-v $(KERNEL_DIR)/out:/kernel/out \
	-v $(BUILD_VOLUME):/build \
	$(BUILD_IMAGE)

.PHONY: kernel kernel-config kernel-builder kernel-clean

kernel: kernel-builder
	mkdir -p $(KERNEL_DIR)/out
	$(DOCKER_RUN) sh /kernel/build.sh image

# Regenerates config-arm64 from arm64 defconfig + config.fragment.
kernel-config: kernel-builder
	mkdir -p $(KERNEL_DIR)/out
	docker run --rm --platform linux/arm64 \
		-v $(KERNEL_DIR):/kernel \
		-v $(BUILD_VOLUME):/build \
		$(BUILD_IMAGE) sh /kernel/build.sh config

kernel-builder:
	docker build --platform linux/arm64 -q -t $(BUILD_IMAGE) $(KERNEL_DIR)

# Drops the source tree, objects and tarball cache.
kernel-clean:
	-docker volume rm $(BUILD_VOLUME)
	-docker image rm $(BUILD_IMAGE)
