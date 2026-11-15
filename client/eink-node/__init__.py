"""
Mirrormere E-Ink Node Client Package
"""

try:
    from .buttons import ButtonHandler
    from .client import EinkClient
    from .coalesce import CoalescingEngine, RefreshResult
    from .config import (
        ButtonsConfig,
        EinkConfig,
        PanelConfig,
        PanelPinsConfig,
        RefreshConfig,
        ServerConfig,
        load_config,
    )
    from .fetcher import ETagFetcher, FetchResult
    from .health import HealthServer
    from .image import load_last_image, save_last_image, stamp_offline_dot, validate_png
    from .offline import OfflineGuard
    from .panel import BasePanel, FakePanel, PanelManager, WaveshareEPDPanel, patch_bonnet_pins
    from .sse import DIRTY_EVENTS, IGNORED_EVENTS, SSEListener
except ImportError:
    from buttons import ButtonHandler
    from coalesce import CoalescingEngine, RefreshResult
    from config import (
        ButtonsConfig,
        EinkConfig,
        PanelConfig,
        PanelPinsConfig,
        RefreshConfig,
        ServerConfig,
        load_config,
    )
    from fetcher import ETagFetcher, FetchResult
    from health import HealthServer
    from image import load_last_image, save_last_image, stamp_offline_dot, validate_png
    from offline import OfflineGuard
    from panel import BasePanel, FakePanel, PanelManager, WaveshareEPDPanel, patch_bonnet_pins
    from sse import DIRTY_EVENTS, IGNORED_EVENTS, SSEListener

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
