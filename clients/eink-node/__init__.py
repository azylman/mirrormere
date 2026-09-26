"""
Mirrormere E-Ink Node Client Package
Reference: SPEC-009 §1-§3
"""

try:
    from .config import EinkConfig, ServerConfig, PanelConfig, PanelPinsConfig, RefreshConfig, ButtonsConfig, load_config
    from .coalesce import CoalescingEngine, RefreshResult
    from .fetcher import ETagFetcher, FetchResult
    from .sse import SSEListener, DIRTY_EVENTS, IGNORED_EVENTS
    from .client import EinkClient
except ImportError:
    from config import EinkConfig, ServerConfig, PanelConfig, PanelPinsConfig, RefreshConfig, ButtonsConfig, load_config
    from coalesce import CoalescingEngine, RefreshResult
    from fetcher import ETagFetcher, FetchResult
    from sse import SSEListener, DIRTY_EVENTS, IGNORED_EVENTS
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
    "EinkClient",
]
