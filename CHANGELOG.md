# Changelog

All notable changes to the Reader Go project will be documented in this file.

## [v0.0.25] - 2026-11-01

### 🚀 Highlights
- **Smooth View Transitions & Instant Shelf**: Implemented module-level caching and background revalidation for the bookshelf view. Returning from reader or detail views renders immediately with zero layout shifts and zero skeleton flashes.
- **Search View Persistence**: Wrapped search in KeepAlive component caching, preventing loss of keywords, search results, and filter selections when toggling between tabs.
- **Automatic Chapter Sub-page Pagination**: Engine automatically fetches, merges, and presents all subsequent sub-pages of multi-page chapters in a unified view.
- **Reading Content Purification**: Added intelligent HTML formatting and regular expression purification rule pipeline to filter ads, noisy markers, and layout disruptions.
- **Dual-Engine Persistence**: Added support for embedded SQLite alongside external MySQL 8.0+, enabling one-click lightweight standalone deployment.
- **Rule Parser Evolution**: Fully expanded rule engine with CSS selectors, XPath 1.0, JSONPath (via GJSON), and safe isolated JavaScript sandbox execution.
- **Speech Synthesizer (TTS)**: Built-in Web Speech API audio reader supporting custom voices, speed/pitch modulation, and auto-scrolling synchronization.
- **Chinese Script Conversion**: Instant client-side Simplified / Traditional Chinese text conversion.
- **Server Chapter Disk Cache**: Dual-tier caching with local server chapter storage (`cache/books/...`) and browser IndexedDB offline storage.
- **Modern UI/UX Overhaul**: Completely modernized user interface with responsive mobile layout, custom fonts, customizable reading themes, and clear visual hierarchy.

---

## [v0.0.16] - 2026-11-01
- Expanded rule parsing capabilities with bracket index, negative index, exclusion index, and multi-line regex clean pipelines.
- Enhanced stability for mobile navigation transitions.

## [v0.0.13] - 2026-11-01
- Expanded table-of-contents pagination limit to 200 pages.
- Improved catalog resolution on large volumes.

## [v0.0.12] - 2026-11-01
- Comprehensive UI/UX redesign with modern visual themes.
- Replaced native browser confirmation dialogs with accessible modal components.

## [v0.0.10] - 2026-10-31
- Added full POST search rule support.
- Expanded concurrent search source limits.

## [v0.0.9] - 2026-10-30
- Multi-architecture container builds (amd64 / arm64).
- Added multi-user book source isolation.
