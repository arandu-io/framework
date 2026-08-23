// The theme: dark or light, and which accent colour.
//
// It is client state and nothing else. The server is never told, no cookie
// carries it, and no request depends on it -- which is what keeps a cached page
// correct for two people who chose differently on the same device.
//
// The first block runs before the body is parsed, on purpose. Waiting for a
// deferred script would paint the page in the default theme and repaint it a
// frame later, and that flash is visible on every navigation. It is also what
// lets the stylesheet swap the theme glyphs with no script at all: the class is
// already on <html> when the first paint happens.
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

	// The same state, without a directive framework. It is read and written from
	// ui.js, which is delegated and cannot run before the body is parsed; this
	// object is what carries the choice across that gap.
	//
	// The current values are not stored here: they are on the html element,
	// which is where they were applied above and where a stylesheet can see
	// them. Reading them back means there is one copy.
	window.arandu = window.arandu || {};
	window.arandu.theme = {
		accents: ACCENTS.slice(),

		get dark() {
			return document.documentElement.classList.contains('dark');
		},

		get accent() {
			return document.documentElement.dataset.theme;
		},

		save() {
			try {
				localStorage.setItem(KEY, JSON.stringify({ dark: this.dark, accent: this.accent }));
			} catch {
				// A browser that refuses storage still gets the theme it asked
				// for; it just does not get it back on the next page.
			}
		},

		toggleDark() {
			document.documentElement.classList.toggle('dark', !this.dark);
			this.save();
		},

		setAccent(next) {
			if (!ACCENTS.includes(next)) return;
			document.documentElement.dataset.theme = next;
			this.save();
		},
	};
})();
