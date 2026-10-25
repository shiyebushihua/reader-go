# Frontend implementation notes

## Design contract
- Mission: a calm Chinese reading room, with an efficient personal library and a deliberately separate administrative workbench.
- Colors: warm paper `#f6f3eb`, surface `#fffdf8`, ink `#242c29`, muted `#747b71`, terracotta action `#a34d35`, line `#deded3`; semantic success `#346b52`, danger `#aa3636`. Reading themes also include white and night.
- Typography: system sans for controls; `Noto Serif SC`, `Songti SC`, `STSong`, Georgia, serif for editorial headings and optional reading text. No remote font dependency.
- Spacing follows 4/8px rhythm; 8px controls, 16px panels, 24px major cards; 1px subtle borders and restrained shadows.
- Motion: immediate press feedback, 160ms color/transform feedback, 240ms state entrance; reduced-motion disables movement.
- Do: honest API states, semantic keyboard controls, explicit error and progress conflict handling, readable Chinese line length, stable responsive grids. Don't: fabricate books/stats, show raw source rules to ordinary users, use unsafe HTML, silently overwrite progress, conceal layout defects.

## Integration assumptions
- All API requests use `/api/v1` and `{data}` / `{error:{code,message}}` envelopes from `../CONTRACT.md`.
- Hash routes require no server fallback. Vite development proxy forwards `/api` to `http://127.0.0.1:8080` (override `VITE_API_PROXY`). Production assets are built into `web/dist`.
- Setup is followed by explicit login; this works whether setup creates a session or not. Password/login/logout mutation responses need not contain a user: `/auth/me` is authoritative.
- Reader progress is a ratio within the current chapter, not an exact text offset or whole-book completion. Saves use the server version and require explicit conflict resolution; navigation saves the current chapter first.
- No remote artwork, fixtures, registration, streaming search, or unsupported backend actions are invented.

## Verification
Implementation and verification details will be appended after build checks.
