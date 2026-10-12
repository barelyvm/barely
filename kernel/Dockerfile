FROM debian:bookworm-slim

RUN apt-get update \
	&& apt-get install -y --no-install-recommends \
		bc bison build-essential ca-certificates cpio curl flex kmod \
		libelf-dev libssl-dev patch perl python3 rsync xz-utils \
	&& rm -rf /var/lib/apt/lists/*
