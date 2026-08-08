// The theme: dark or light, and which accent colour.
//
// It is client state and nothing else. The server is never told, no cookie
// carries it, and no request depends on it -- which is what RULE 13 allows
// Alpine for, and what keeps a cached page correct for two people who chose
// differently on the same device.
//
// The first block runs before the body is parsed, on purpose. Waiting for Alpine
// would paint the page in the default theme and repaint it a frame later, and
// that flash is visible on every navigation.
(() => {
	const KEY = 'arandu.theme';
	const ACCENTS = ['slate', 'blue', 'green', 'amber', 'rose', 'violet'];

	const stored = () => {
		try {
			return JSON.parse(localStorage.getItem(KEY)) || {};
		} catch {
			// A corrupt entry is not worth a broken page. The default is fine and
			// the next write repairs it.
			return {};
		}
	};

	const saved = stored();
	const dark = saved.dark ?? window.matchMedia('(prefers-color-scheme: dark)').matches;
	const accent = ACCENTS.includes(saved.accent) ? saved.accent : ACCENTS[0];

	// Applied to the element that is already parsed. document.body does not exist
	// yet at this point; documentElement does.
	document.documentElement.classList.toggle('dark', dark);
	document.documentElement.dataset.theme = accent;

	document.addEventListener('alpine:init', () => {
		Alpine.store('theme', {
			accents: ACCENTS,
			dark,
			accent,

			save() {
				localStorage.setItem(KEY, JSON.stringify({ dark: this.dark, accent: this.accent }));
			},
			toggleDark() {
				this.dark = !this.dark;
				document.documentElement.classList.toggle('dark', this.dark);
				this.save();
			},
			setAccent(next) {
				if (!ACCENTS.includes(next)) return;
				this.accent = next;
				document.documentElement.dataset.theme = next;
				this.save();
			},
		});
	});
})();
