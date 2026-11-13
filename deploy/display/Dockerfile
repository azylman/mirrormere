# syntax=docker/dockerfile:1
FROM mirror.gcr.io/library/alpine:3.24

LABEL org.opencontainers.image.source="https://github.com/azylman/mirrormere"

RUN apk add --no-cache \
        bash \
        ca-certificates \
        tzdata \
        cage \
        chromium \
        wlr-randr \
        swayidle \
        kanshi \
        seatd \
        pipewire \
        pipewire-alsa \
        pipewire-pulse \
        pipewire-tools \
        pipewire-spa-tools \
        pipewire-filter-graph \
        pipewire-filter-graph-builtin \
        wireplumber \
        pulseaudio-utils \
        alsa-utils \
        dbus \
        eudev \
        runuser \
        shadow \
        mesa-dri-gallium \
        mesa-va-gallium \
        mesa-egl \
        mesa-gles \
        font-dejavu \
        font-noto \
        font-noto-emoji \
    && if [ "$(uname -m)" = "x86_64" ]; then apk add --no-cache intel-media-driver; fi

RUN printf 'pcm.!default {\n    type pulse\n}\nctl.!default {\n    type pulse\n}\n' > /etc/asound.conf \
    && if [ ! -f /usr/bin/Xwayland ]; then \
        printf '#!/bin/sh\nexit 0\n' > /usr/bin/Xwayland && \
        chmod 0755 /usr/bin/Xwayland; \
    fi

RUN for g in render video input audio seat; do \
        getent group "$g" >/dev/null || groupadd -r "$g"; \
    done && \
    getent group kiosk >/dev/null || groupadd -g 10001 kiosk && \
    id -u kiosk >/dev/null 2>&1 || useradd -u 10001 -g 10001 -m -d /home/kiosk -s /bin/bash -G video,input,audio,render,seat kiosk

RUN mkdir -p /opt/mirrormere/display \
             /etc/kiosk/init.d \
             /etc/pipewire/pipewire.conf.d \
             /run/kiosk/pulse \
             /data/chrome-profile \
             /home/kiosk && \
    chown -R 10001:10001 /run/kiosk /data/chrome-profile /home/kiosk

COPY entrypoint.sh launch.sh session.sh dpms.sh /opt/mirrormere/display/
COPY default-mirrormere-display /etc/default/mirrormere-display

RUN chmod 0755 /opt/mirrormere/display/*.sh && \
    chmod 0644 /etc/default/mirrormere-display

ENV XDG_RUNTIME_DIR=/run/kiosk \
    WAYLAND_DISPLAY=wayland-0 \
    PULSE_SERVER=unix:/run/kiosk/pulse/native \
    MIRRORMERE_CHROME_USER_DATA_DIR=/data/chrome-profile \
    MIRRORMERE_REMOTE_DEBUGGING_PORT=9222

EXPOSE 9222

ENTRYPOINT ["/opt/mirrormere/display/entrypoint.sh"]
