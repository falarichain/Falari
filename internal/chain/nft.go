package chain

// GenerateDefaultNFTTemplate returns a deterministic SVG template string
// used as the initial NFT badge for miners. It contains {{MINER_ID}} and
// {{MINER_ADDR}} placeholders that the frontend replaces per-miner.
func GenerateDefaultNFTTemplate() string {
	return defaultNFTSVG
}

const defaultNFTSVG = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 400 400">
  <defs>
    <radialGradient id="bg" cx="50%" cy="45%" r="65%">
      <stop offset="0%" stop-color="#1a2a3a"/>
      <stop offset="100%" stop-color="#0a0e14"/>
    </radialGradient>
    <linearGradient id="frame" x1="0" y1="0" x2="1" y2="1">
      <stop offset="0%" stop-color="#3af5c8"/>
      <stop offset="50%" stop-color="#1e88e5"/>
      <stop offset="100%" stop-color="#7c4dff"/>
    </linearGradient>
    <linearGradient id="glow" x1="0" y1="0" x2="0" y2="1">
      <stop offset="0%" stop-color="#3af5c8" stop-opacity="0.3"/>
      <stop offset="100%" stop-color="#1e88e5" stop-opacity="0"/>
    </linearGradient>
  </defs>

  <!-- Background -->
  <rect width="400" height="400" rx="24" fill="url(#bg)"/>

  <!-- Grid dots -->
  <g opacity="0.08" fill="#3af5c8">
    <circle cx="40" cy="40" r="1.2"/><circle cx="80" cy="40" r="1.2"/>
    <circle cx="120" cy="40" r="1.2"/><circle cx="160" cy="40" r="1.2"/>
    <circle cx="200" cy="40" r="1.2"/><circle cx="240" cy="40" r="1.2"/>
    <circle cx="280" cy="40" r="1.2"/><circle cx="320" cy="40" r="1.2"/>
    <circle cx="360" cy="40" r="1.2"/>
    <circle cx="40" cy="80" r="1.2"/><circle cx="80" cy="80" r="1.2"/>
    <circle cx="120" cy="80" r="1.2"/><circle cx="160" cy="80" r="1.2"/>
    <circle cx="200" cy="80" r="1.2"/><circle cx="240" cy="80" r="1.2"/>
    <circle cx="280" cy="80" r="1.2"/><circle cx="320" cy="80" r="1.2"/>
    <circle cx="360" cy="80" r="1.2"/>
    <circle cx="40" cy="120" r="1.2"/><circle cx="80" cy="120" r="1.2"/>
    <circle cx="320" cy="120" r="1.2"/><circle cx="360" cy="120" r="1.2"/>
    <circle cx="40" cy="280" r="1.2"/><circle cx="80" cy="280" r="1.2"/>
    <circle cx="320" cy="280" r="1.2"/><circle cx="360" cy="280" r="1.2"/>
    <circle cx="40" cy="320" r="1.2"/><circle cx="80" cy="320" r="1.2"/>
    <circle cx="120" cy="320" r="1.2"/><circle cx="160" cy="320" r="1.2"/>
    <circle cx="200" cy="320" r="1.2"/><circle cx="240" cy="320" r="1.2"/>
    <circle cx="280" cy="320" r="1.2"/><circle cx="320" cy="320" r="1.2"/>
    <circle cx="360" cy="320" r="1.2"/>
    <circle cx="40" cy="360" r="1.2"/><circle cx="80" cy="360" r="1.2"/>
    <circle cx="120" cy="360" r="1.2"/><circle cx="160" cy="360" r="1.2"/>
    <circle cx="200" cy="360" r="1.2"/><circle cx="240" cy="360" r="1.2"/>
    <circle cx="280" cy="360" r="1.2"/><circle cx="320" cy="360" r="1.2"/>
    <circle cx="360" cy="360" r="1.2"/>
  </g>

  <!-- Corner brackets -->
  <g stroke="#3af5c8" stroke-width="1.5" fill="none" opacity="0.4">
    <polyline points="30,55 30,30 55,30"/>
    <polyline points="345,30 370,30 370,55"/>
    <polyline points="370,345 370,370 345,370"/>
    <polyline points="55,370 30,370 30,345"/>
  </g>

  <!-- Circuit traces -->
  <g stroke="#1e88e5" stroke-width="0.8" fill="none" opacity="0.25">
    <line x1="55" y1="30" x2="120" y2="30"/>
    <line x1="280" y1="30" x2="345" y2="30"/>
    <line x1="30" y1="55" x2="30" y2="120"/>
    <line x1="370" y1="55" x2="370" y2="120"/>
    <line x1="55" y1="370" x2="120" y2="370"/>
    <line x1="280" y1="370" x2="345" y2="370"/>
    <line x1="30" y1="280" x2="30" y2="345"/>
    <line x1="370" y1="280" x2="370" y2="345"/>
  </g>

  <!-- Hexagonal frame -->
  <polygon points="200,60 310,115 310,225 200,280 90,225 90,115"
           fill="none" stroke="url(#frame)" stroke-width="2" opacity="0.6"/>
  <polygon points="200,72 298,122 298,218 200,268 102,218 102,122"
           fill="url(#glow)" stroke="none"/>

  <!-- Inner decorative hex -->
  <polygon points="200,85 285,130 285,210 200,255 115,210 115,130"
           fill="none" stroke="#3af5c8" stroke-width="0.5" opacity="0.2"/>

  <!-- Top label -->
  <text x="200" y="50" text-anchor="middle" fill="#3af5c8" font-family="monospace"
        font-size="10" letter-spacing="4" opacity="0.6">FALARI NETWORK</text>

  <!-- Miner ID (placeholder) -->
  <text x="200" y="185" text-anchor="middle" fill="#e0f7fa" font-family="monospace"
        font-size="42" font-weight="bold" letter-spacing="2">{{MINER_ID}}</text>

  <!-- Address (placeholder) -->
  <text x="200" y="215" text-anchor="middle" fill="#78909c" font-family="monospace"
        font-size="13">{{MINER_ADDR}}</text>

  <!-- Divider line -->
  <line x1="140" y1="230" x2="260" y2="230" stroke="#3af5c8" stroke-width="0.5" opacity="0.3"/>

  <!-- Bottom label -->
  <text x="200" y="252" text-anchor="middle" fill="#1e88e5" font-family="monospace"
        font-size="14" letter-spacing="6" font-weight="bold">FALARI MINER</text>

  <!-- Bottom decorative elements -->
  <g opacity="0.3">
    <rect x="155" y="300" width="90" height="1" fill="#3af5c8"/>
    <circle cx="155" cy="300" r="2" fill="#3af5c8"/>
    <circle cx="245" cy="300" r="2" fill="#3af5c8"/>
  </g>

  <!-- Storage icon -->
  <g transform="translate(185,310)" fill="none" stroke="#3af5c8" stroke-width="1" opacity="0.4">
    <rect x="0" y="0" width="30" height="8" rx="2"/>
    <rect x="0" y="12" width="30" height="8" rx="2"/>
    <rect x="0" y="24" width="30" height="8" rx="2"/>
    <circle cx="24" cy="4" r="1.5" fill="#3af5c8"/>
    <circle cx="24" cy="16" r="1.5" fill="#3af5c8"/>
    <circle cx="24" cy="28" r="1.5" fill="#3af5c8"/>
  </g>

  <!-- Bottom text -->
  <text x="200" y="375" text-anchor="middle" fill="#546e7a" font-family="monospace"
        font-size="9" letter-spacing="2">DECENTRALIZED STORAGE</text>
</svg>`
