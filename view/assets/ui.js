/* Arandu client behaviours.
 *
 * Everything interactive on a page is bound once, on `document`, and dispatched
 * by looking at `data-*` attributes on the element an event came from. Nothing
 * here reads an attribute and evaluates it: attributes carry data, never code.
 *
 * That is the whole reason this file exists rather than Alpine. Alpine compiles
 * every directive expression with `new AsyncFunction`, and this framework serves
 * `script-src 'self'` with no `unsafe-eval`, so every directive threw at the
 * point of evaluation and no client behaviour ever ran. Loosening the policy
 * would buy the behaviour back at the price of the policy, and building the same
 * evaluator by hand would reopen the same hole under a different name.
 *
 * Because it is delegation, markup that HTMX swaps in is live the moment it
 * lands: there is nothing to initialise and nothing to tear down. The one sweep
 * below stamps state that only the browser knows -- which accent is in force --
 * onto attributes a stylesheet and a screen reader can see. Skipping the sweep
 * costs a checkmark, never an interaction.
 *
 * It reads no expression, keeps no parallel state, and stores nothing per
 * element: open, active and selected all live in the ARIA the markup already
 * has to carry, so the DOM is the state and there is only one copy of it.
 *
 * Loading this file twice is a no-op.
 */
(function () {
	'use strict';

	var arandu = window.arandu = window.arandu || {};
	if (arandu.ui) return;
	arandu.ui = { version: 1 };

	var OPTION = '[role="option"]';
	var LINE = '[role="menuitem"]';
	var COPY_RESET = 1600;

	var copyTimers = new WeakMap();

	/* The element an event came from, or null when it was not an element. */
	function origin(event) {
		var node = event.target;
		return node && node.nodeType === 1 ? node : null;
	}

	/* Whether an item can be reached: not disabled, and not filtered away. */
	function usable(item) {
		return !!item &&
			item.getAttribute('aria-disabled') !== 'true' &&
			item.getAttribute('aria-hidden') !== 'true' &&
			!item.hasAttribute('disabled');
	}

	function list(container, selector) {
		if (!container) return [];
		var found = container.querySelectorAll(selector);
		var out = [];
		for (var i = 0; i < found.length; i++) {
			if (usable(found[i])) out.push(found[i]);
		}
		return out;
	}

	/* ---- active item -------------------------------------------------------
	 *
	 * Which item is active is held in one place, aria-activedescendant on the
	 * text box, because that is the attribute a screen reader reads. The class
	 * on the item is what the stylesheet paints, and it is kept in step here so
	 * the two can never disagree.
	 */

	function activeID(input) {
		return input ? input.getAttribute('aria-activedescendant') : null;
	}

	function setActive(input, item, container, selector, scroll) {
		if (container) {
			var all = container.querySelectorAll(selector);
			for (var i = 0; i < all.length; i++) {
				if (all[i] !== item) all[i].classList.remove('active');
			}
		}
		if (!item) {
			if (input) input.removeAttribute('aria-activedescendant');
			return;
		}
		item.classList.add('active');
		if (input) {
			if (item.id) input.setAttribute('aria-activedescendant', item.id);
			else input.removeAttribute('aria-activedescendant');
		}
		if (scroll && item.scrollIntoView) item.scrollIntoView({ block: 'nearest' });
	}

	function move(input, container, selector, step) {
		var all = list(container, selector);
		if (!all.length) return;

		var current = activeID(input);
		var at = -1;
		for (var i = 0; i < all.length; i++) {
			if (all[i].id && all[i].id === current) { at = i; break; }
		}
		var to = at < 0 ? (step > 0 ? 0 : all.length - 1) : (at + step + all.length) % all.length;
		setActive(input, all[to], container, selector, true);
	}

	/* ---- theme -------------------------------------------------------------
	 *
	 * The state itself is theme.js's: it applies the choice to <html> before the
	 * body is parsed and owns what is written to storage. This only asks it to
	 * change, and reflects the answer onto the buttons.
	 */

	function theme() {
		return arandu.theme || null;
	}

	function setMode(mode) {
		var store = theme();
		if (!mode || !store || typeof store.set !== 'function') return;
		store.set(mode);
		stamp(document);
	}

	/* ---- copy --------------------------------------------------------------
	 *
	 * The text is read out of an attribute and handed to the clipboard as text.
	 * Which of the two words the button reads is the stylesheet's, keyed on the
	 * marker set here.
	 */

	function copy(button) {
		var text = button.getAttribute('data-copy-text');
		if (text === null || !navigator.clipboard) return;

		navigator.clipboard.writeText(text).then(function () {
			window.clearTimeout(copyTimers.get(button));
			button.setAttribute('data-copied', '');
			copyTimers.set(button, window.setTimeout(function () {
				button.removeAttribute('data-copied');
				copyTimers.delete(button);
			}, COPY_RESET));
		}, function () {
			/* No clipboard permission, or an insecure origin. The line is on the
			 * page and can be selected; saying nothing beats saying "copied" to
			 * somebody whose clipboard is empty. */
		});
	}

	/* ---- combobox ----------------------------------------------------------
	 *
	 * The list comes from the server on every keystroke, over HTMX, and is
	 * swapped into the listbox whole. Nothing here filters and nothing here
	 * fetches: this opens and closes the popover, walks the options that are
	 * present, and writes the chosen one into the two inputs.
	 */

	function comboboxParts(box) {
		return {
			input: box.querySelector('input[role="combobox"]'),
			listbox: box.querySelector('[role="listbox"]'),
			popover: box.querySelector('[data-popover]'),
			value: box.querySelector('[data-combobox-value]')
		};
	}

	function setComboboxOpen(box, open) {
		var parts = comboboxParts(box);
		if (parts.input) parts.input.setAttribute('aria-expanded', open ? 'true' : 'false');
		if (parts.popover) parts.popover.setAttribute('aria-hidden', open ? 'false' : 'true');
		if (!open) setActive(parts.input, null, parts.listbox, OPTION, false);
	}

	function comboboxOpen(box) {
		var input = box.querySelector('input[role="combobox"]');
		return !!input && input.getAttribute('aria-expanded') === 'true';
	}

	function chooseOption(box, option) {
		if (!usable(option)) return;
		var parts = comboboxParts(box);

		if (parts.listbox) {
			var all = parts.listbox.querySelectorAll(OPTION);
			for (var i = 0; i < all.length; i++) {
				if (all[i] === option) all[i].setAttribute('aria-selected', 'true');
				else all[i].removeAttribute('aria-selected');
			}
		}
		if (parts.value) parts.value.value = option.getAttribute('data-value') || '';
		if (parts.input) {
			var label = option.getAttribute('data-label');
			parts.input.value = label === null ? option.textContent.trim() : label;
		}
		setComboboxOpen(box, false);
	}

	function comboboxKey(event, box, input) {
		var parts = comboboxParts(box);

		if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
			event.preventDefault();
			setComboboxOpen(box, true);
			move(input, parts.listbox, OPTION, event.key === 'ArrowDown' ? 1 : -1);
			return;
		}
		if (event.key === 'Enter') {
			if (input.getAttribute('aria-expanded') !== 'true') return;
			event.preventDefault();
			var id = activeID(input);
			var option = id ? document.getElementById(id) : null;
			if (option && parts.listbox && parts.listbox.contains(option)) chooseOption(box, option);
			return;
		}
		if (event.key === 'Escape') setComboboxOpen(box, false);
	}

	/* ---- command palette ---------------------------------------------------
	 *
	 * Every line is in the document from the first paint and stays there. What
	 * is typed sets aria-hidden on the lines it does not match, and the
	 * stylesheet does the rest: it hides a hidden line, hides a group whose
	 * lines all went, and draws the empty message when none are left. Nothing is
	 * fetched, nothing is removed, and the search box's own value is the query
	 * -- there is no second copy of it to fall out of step.
	 */

	function commandParts(palette) {
		return {
			input: palette.querySelector('input[role="combobox"]'),
			menu: palette.querySelector('[role="menu"]')
		};
	}

	function filterCommand(palette) {
		var parts = commandParts(palette);
		if (!parts.menu) return;

		var query = parts.input ? parts.input.value.trim().toLowerCase() : '';
		var lines = parts.menu.querySelectorAll(LINE);
		for (var i = 0; i < lines.length; i++) {
			var line = lines[i];
			var haystack = (line.getAttribute('data-search') || line.textContent || '').toLowerCase();
			if (query && haystack.indexOf(query) === -1) line.setAttribute('aria-hidden', 'true');
			else line.removeAttribute('aria-hidden');
		}

		/* A line that was active and has just been filtered away is a line the
		 * Enter key would still follow. */
		var current = activeID(parts.input);
		var active = current ? document.getElementById(current) : null;
		if (current && (!active || !parts.menu.contains(active) || !usable(active))) {
			setActive(parts.input, null, parts.menu, LINE, false);
		}
	}

	function commandKey(event, palette, input) {
		var parts = commandParts(palette);

		if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
			event.preventDefault();
			move(input, parts.menu, LINE, event.key === 'ArrowDown' ? 1 : -1);
			return;
		}
		if (event.key === 'Enter') {
			event.preventDefault();
			var id = activeID(input);
			var line = id ? document.getElementById(id) : null;
			if (line && parts.menu && parts.menu.contains(line) && usable(line)) line.click();
			return;
		}
		if (event.key === 'Escape') {
			input.value = '';
			filterCommand(palette);
			setActive(input, null, parts.menu, LINE, false);
		}
	}

	/* ---- range slider ------------------------------------------------------
	 *
	 * The server draws the filled track and the number, so a slider is right
	 * before this runs and stays right if it never does. This keeps both in step
	 * with a thumb that is moving.
	 */

	function paintSlider(track) {
		var min = Number(track.min);
		var max = Number(track.max);
		var value = Number(track.value);
		if (!isFinite(min)) min = 0;
		if (!isFinite(max)) max = 100;
		if (!isFinite(value)) value = min;

		var span = max - min;
		var fill = span > 0 ? ((value - min) / span) * 100 : 0;
		track.style.setProperty('--slider-value', fill + '%');

		var field = track.closest('[data-slider]');
		var output = field ? field.querySelector('[data-slider-output]') : null;
		if (output) output.textContent = track.value;
	}

	/* ---- the sweep ---------------------------------------------------------
	 *
	 * Two things a server cannot write and delegation cannot reach, because they
	 * are state rather than events. Which accent is in force lives in the
	 * browser, so no cached page can carry aria-current on the right button; and
	 * the vendored stylesheet paints a hover highlight of its own until a
	 * component says it is driving one, which is what the initialised marker
	 * says. Both are idempotent, and neither gates any behaviour.
	 */

	function each(scope, selector, fn) {
		if (scope.nodeType === 1 && scope.matches(selector)) fn(scope);
		var found = scope.querySelectorAll(selector);
		for (var i = 0; i < found.length; i++) fn(found[i]);
	}

	function stamp(scope) {
		var node = scope && (scope.nodeType === 1 || scope.nodeType === 9) ? scope : document;
		var mode = document.documentElement.getAttribute('data-theme') || 'auto';

		each(node, '[data-theme-mode]', function (button) {
			button.setAttribute('aria-checked', button.getAttribute('data-theme-mode') === mode ? 'true' : 'false');
		});
		each(node, '[data-combobox]', function (box) {
			box.setAttribute('data-combobox-initialized', 'true');
		});
		each(node, '[data-command]', function (palette) {
			palette.setAttribute('data-command-initialized', 'true');
		});
	}

	/* ---- delegation --------------------------------------------------------
	 *
	 * Four listeners, all on document, all reading attributes rather than
	 * running them.
	 */

	document.addEventListener('click', function (event) {
		var from = origin(event);
		if (!from) return;

		var copyButton = from.closest('[data-copy]');
		if (copyButton) copy(copyButton);

		var mode = from.closest('[data-theme-mode]');
		if (mode) setMode(mode.getAttribute('data-theme-mode'));

		var option = from.closest('[data-combobox] ' + OPTION);
		if (option) chooseOption(option.closest('[data-combobox]'), option);

		/* Clicking away closes: the outside click every combobox on the page
		 * cares about, decided by containment rather than by a listener each. */
		var boxes = document.querySelectorAll('[data-combobox]');
		for (var i = 0; i < boxes.length; i++) {
			if (!boxes[i].contains(from) && comboboxOpen(boxes[i])) setComboboxOpen(boxes[i], false);
		}

		var input = from.closest('[data-combobox] input[role="combobox"]');
		if (input) setComboboxOpen(input.closest('[data-combobox]'), true);
	});

	document.addEventListener('input', function (event) {
		var from = origin(event);
		if (!from) return;

		var track = from.closest('[data-slider-track]');
		if (track) paintSlider(track);

		var boxInput = from.closest('[data-combobox] input[role="combobox"]');
		if (boxInput) {
			var box = boxInput.closest('[data-combobox]');
			var parts = comboboxParts(box);
			setComboboxOpen(box, true);
			setActive(boxInput, null, parts.listbox, OPTION, false);
		}

		var paletteInput = from.closest('[data-command] input[role="combobox"]');
		if (paletteInput) filterCommand(paletteInput.closest('[data-command]'));
	});

	document.addEventListener('keydown', function (event) {
		var from = origin(event);
		if (!from || !from.matches('input[role="combobox"]')) return;

		var box = from.closest('[data-combobox]');
		if (box) { comboboxKey(event, box, from); return; }

		var palette = from.closest('[data-command]');
		if (palette) commandKey(event, palette, from);
	});

	/* The mouse moves the active item so that the keyboard and the pointer
	 * never highlight two different rows. */
	document.addEventListener('pointermove', function (event) {
		var from = origin(event);
		if (!from) return;

		var option = from.closest('[data-combobox] ' + OPTION);
		if (option) {
			if (!usable(option)) return;
			var box = option.closest('[data-combobox]');
			var parts = comboboxParts(box);
			if (activeID(parts.input) !== option.id) {
				setActive(parts.input, option, parts.listbox, OPTION, false);
			}
			return;
		}

		var line = from.closest('[data-command] ' + LINE);
		if (line) {
			if (!usable(line)) return;
			var palette = line.closest('[data-command]');
			var pieces = commandParts(palette);
			if (activeID(pieces.input) !== line.id) {
				setActive(pieces.input, line, pieces.menu, LINE, false);
			}
		}
	});

	if (document.readyState === 'loading') {
		document.addEventListener('DOMContentLoaded', function () { stamp(document); });
	} else {
		stamp(document);
	}
	document.addEventListener('htmx:load', function (event) { stamp(event.target); });
})();
