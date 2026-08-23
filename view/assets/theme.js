// The theme: light, dark, or whatever the device says.
//
// It is client state and nothing else. The server is never told, no cookie
// carries it, and no request depends on it -- which is what keeps a cached page
// correct for two people who chose differently on the same device.
//
// Three modes rather than a switch. A switch has to start somewhere, and
// wherever it starts is wrong for half the people arriving: somebody whose
// device is dark and whose last visit left it light gets a white page, and
// nothing on the control says why. "auto" is the state where the page follows
// the device, and it is the default -- so the first visit is right without
// anybody choosing, and choosing is how you stop following.
//
// The first block runs before the body is parsed, on purpose. Waiting for a
// deferred script would paint the page in the wrong theme and repaint it a frame
// later, and that flash is visible on every navigation. It is also what lets the
// stylesheet swap the icons with no script at all: the class is already on
// <html> when the first paint happens.
(() => {
	const KEY = 'arandu.theme';
	const MODES = ['auto', 'light', 'dark'];

	const media = window.matchMedia('(prefers-color-scheme: dark)');

	const stored = () => {
		try {
			const saved = localStorage.getItem(KEY);
			return MODES.includes(saved) ? saved : MODES[0];
		} catch {
			// A corrupt entry, or a browser that refuses storage, is not worth a
			// broken page. Following the device is the answer that needs nothing.
			return MODES[0];
		}
	};

	// apply writes both facts to the element that is already parsed: which mode
	// was chosen, and whether that mode is dark right now. document.body does not
	// exist at this point; documentElement does.
	//
	// Both are needed and neither implies the other. The stylesheet paints from
	// the class; the control marks which of its three options is chosen from the
	// attribute. In auto they disagree half the time, which is the point.
	const apply = (mode) => {
		const dark = mode === 'dark' || (mode === 'auto' && media.matches);
		document.documentElement.classList.toggle('dark', dark);
		document.documentElement.dataset.theme = mode;
	};

	apply(stored());

	// The device can change its mind while the page is open -- at sunset, on a
	// schedule, or because somebody flipped a system switch. A page in auto
	// follows; a page that chose does not.
	media.addEventListener('change', () => {
		if (document.documentElement.dataset.theme === 'auto') apply('auto');
	});

	// The same state, without a directive framework. It is read and written from
	// ui.js, which is delegated and cannot run before the body is parsed; this
	// object is what carries the choice across that gap.
	//
	// The current mode is not kept here: it is on the html element, which is
	// where it was applied above and where a stylesheet can see it. Reading it
	// back means there is one copy.
	window.arandu = window.arandu || {};
	window.arandu.theme = {
		modes: MODES.slice(),

		get mode() {
			return document.documentElement.dataset.theme || MODES[0];
		},

		set(next) {
			if (!MODES.includes(next)) return;
			apply(next);
			try {
				localStorage.setItem(KEY, next);
			} catch {
				// A browser that refuses storage still gets the theme it asked
				// for; it just does not get it back on the next page.
			}
		},
	};
})();
