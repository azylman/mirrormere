"""
Mirrormere E-Ink Node Client Package
Reference: SPEC-009 §1-§8
"""

try:
    from .config import (
        EinkConfig,
        ServerConfig,
        PanelConfig,
        PanelPinsConfig,
        RefreshConfig,
        ButtonsConfig,
        load_config,
    )
    from .coalesce import CoalescingEngine, RefreshResult
    from .fetcher import ETagFetcher, FetchResult
    from .sse import SSEListener, DIRTY_EVENTS, IGNORED_EVENTS
    from .image import validate_png, stamp_offline_dot, save_last_image, load_last_image
    from .panel import BasePanel, FakePanel, WaveshareEPDPanel, PanelManager, patch_bonnet_pins
    from .offline import OfflineGuard
    from .buttons import ButtonHandler
    from .health import HealthServer
    from .client import EinkClient
except ImportError:
    from config import (
        EinkConfig,
        ServerConfig,
        PanelConfig,
        PanelPinsConfig,
        RefreshConfig,
        ButtonsConfig,
        load_config,
    )
    from coalesce import CoalescingEngine, RefreshResult
    from fetcher import ETagFetcher, FetchResult
    from sse import SSEListener, DIRTY_EVENTS, IGNORED_EVENTS
    from image import validate_png, stamp_offline_dot, save_last_image, load_last_image
    from panel import BasePanel, FakePanel, WaveshareEPDPanel, PanelManager, patch_bonnet_pins
    from offline import OfflineGuard
    from buttons import ButtonHandler
    from health import HealthServer
    from client import EinkClient

__all__ = [
    "EinkConfig",
    "ServerConfig",
    "PanelConfig",
    "PanelPinsConfig",
    "RefreshConfig",
    "ButtonsConfig",
    "load_config",
    "CoalescingEngine",
    "RefreshResult",
    "ETagFetcher",
    "FetchResult",
    "SSEListener",
    "DIRTY_EVENTS",
    "IGNORED_EVENTS",
    "validate_png",
    "stamp_offline_dot",
    "save_last_image",
    "load_last_image",
    "BasePanel",
    "FakePanel",
    "WaveshareEPDPanel",
    "PanelManager",
    "patch_bonnet_pins",
    "OfflineGuard",
    "ButtonHandler",
    "HealthServer",
    "EinkClient",
]
