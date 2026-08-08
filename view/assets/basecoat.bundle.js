/* Basecoat -- MIT, Copyright (c) 2025 Ronan Berder.
 *
 * Vendored, not installed: there is no package.json in an Arandu project and
 * the CSP is script-src 'self', so a CDN would not run even if one were
 * referenced. The full licence ships with the stylesheet, in
 * resources/css/basecoat/LICENSE.md.
 *
 * The registry comes first; the components register into it.
 */

/* --- basecoat --- */
(() => {
  const componentRegistry = {};
  let observer = null;

  const registerComponent = (name, selectorOrOptions, initFunction) => {
    const options = typeof selectorOrOptions === 'object'
      ? selectorOrOptions
      : { selector: selectorOrOptions, init: initFunction };

    componentRegistry[name] = {
      selector: options.selector,
      init: options.init,
      refresh: options.refresh,
    };
  };

  const initComponent = (element, componentName) => {
    const component = componentRegistry[componentName];
    if (!component) return;

    try {
      component.init(element);
      if (element.hasAttribute(`data-${componentName}-initialized`)) {
        element.dataset.basecoatComponent = componentName;
      }
    } catch (error) {
      console.error(`Failed to initialize ${componentName}:`, error);
      if (typeof element._destroy === 'function') {
        try {
          element._destroy();
        } catch (destroyError) {
          console.error(`Failed to clean up ${componentName} after initialization error:`, destroyError);
        }
      }
      delete element._destroy;
      element.removeAttribute(`data-${componentName}-initialized`);
      delete element.dataset.basecoatComponent;
    }
  };

  const destroyComponent = (element) => {
    if (!element || element.nodeType !== Node.ELEMENT_NODE) return;
    const componentName = element.dataset?.basecoatComponent;

    if (typeof element._destroy === 'function') {
      try {
        element._destroy();
      } catch (error) {
        console.error('Failed to destroy Basecoat component:', error);
      }
    }

    delete element._destroy;
    if (componentName) element.removeAttribute(`data-${componentName}-initialized`);
    delete element.dataset.basecoatComponent;
  };

  const destroyRemovedComponents = (node) => {
    if (node.nodeType !== Node.ELEMENT_NODE) return;
    if (node.isConnected) return;

    if (node.dataset?.basecoatComponent) destroyComponent(node);
    node.querySelectorAll('[data-basecoat-component]').forEach(destroyComponent);
  };

  const uniqueElements = (elements) => Array.from(new Set(elements));

  const getComponentElements = (componentName, selector, force = false) => {
    const elements = Array.from(document.querySelectorAll(selector));
    if (force) {
      elements.push(...document.querySelectorAll(`[data-basecoat-component="${componentName}"]`));
    }
    return uniqueElements(elements);
  };

  const initAllComponents = (options = {}) => {
    const force = options.force === true;
    Object.entries(componentRegistry).forEach(([name, { selector }]) => {
      getComponentElements(name, selector, force).forEach((element) => {
        const wasComponent = element.dataset?.basecoatComponent === name;
        if (force) destroyComponent(element);
        if (wasComponent || element.matches(selector)) initComponent(element, name);
      });
    });
  };

  const initNewComponents = (node) => {
    if (node.nodeType !== Node.ELEMENT_NODE) return;

    Object.entries(componentRegistry).forEach(([name, { selector }]) => {
      if (node.matches(selector)) initComponent(node, name);
      node.querySelectorAll(selector).forEach(element => initComponent(element, name));
    });
  };

  const refreshComponent = (element) => {
    if (!element) return;
    if (typeof element.refresh === 'function') {
      element.refresh();
      return;
    }

    const componentName = element.dataset?.basecoatComponent;
    const component = componentName ? componentRegistry[componentName] : null;
    if (component?.refresh) {
      component.refresh(element);
    }
  };

  const startObserver = () => {
    if (observer) return;

    observer = new MutationObserver((mutations) => {
      mutations.forEach((mutation) => {
        mutation.addedNodes.forEach(initNewComponents);
        mutation.removedNodes.forEach(destroyRemovedComponents);
      });
    });

    observer.observe(document.body, { childList: true, subtree: true });
  };

  const stopObserver = () => {
    if (!observer) return;
    observer.disconnect();
    observer = null;
  };

  const initRegisteredComponent = (componentName, options = {}) => {
    const component = componentRegistry[componentName];
    if (!component) {
      console.warn(`Component '${componentName}' not found in registry`);
      return;
    }

    const force = options.force === true;
    getComponentElements(componentName, component.selector, force).forEach((element) => {
      const wasComponent = element.dataset?.basecoatComponent === componentName;
      if (force) destroyComponent(element);
      if (wasComponent || element.matches(component.selector)) initComponent(element, componentName);
    });
  };

  const initAllRegisteredComponents = (options = {}) => {
    initAllComponents(options);
  };

  const setTheme = (mode) => {
    const dark = mode === 'dark';
    document.documentElement.classList.toggle('dark', dark);
    try { localStorage.setItem('themeMode', dark ? 'dark' : 'light'); } catch (_) {}
    document.dispatchEvent(new CustomEvent('basecoat:themechange', { detail: { mode: dark ? 'dark' : 'light' } }));
  };

  const getTheme = () => document.documentElement.classList.contains('dark') ? 'dark' : 'light';

  window.basecoat = {
    register: registerComponent,
    init: initRegisteredComponent,
    initAll: initAllRegisteredComponents,
    refresh: refreshComponent,
    start: startObserver,
    stop: stopObserver,
    theme: {
      get: getTheme,
      set: setTheme,
      toggle: () => setTheme(getTheme() === 'dark' ? 'light' : 'dark'),
    },
  };

  document.addEventListener('DOMContentLoaded', () => {
    initAllComponents();
    startObserver();
  });
})();

/* --- dropdown-menu --- */
(() => {
  const states = new WeakMap();

  const isDisabled = (item) =>
    item.hasAttribute('disabled') || item.getAttribute('aria-disabled') === 'true';

  const getElements = (root) => {
    const trigger = root.querySelector(':scope > button');
    const popover = root.querySelector(':scope > [data-popover]');
    const menu = popover ? popover.querySelector('[role="menu"]') : null;
    return { trigger, popover, menu };
  };

  const getItems = (menu) => Array.from(menu.querySelectorAll('[role^="menuitem"]')).filter(item => !isDisabled(item));

  const setActiveItem = (state, index) => {
    if (state.activeIndex > -1 && state.items[state.activeIndex]) {
      state.items[state.activeIndex].classList.remove('active');
    }
    state.activeIndex = index;
    if (state.activeIndex > -1 && state.items[state.activeIndex]) {
      const activeItem = state.items[state.activeIndex];
      activeItem.classList.add('active');
      if (activeItem.id) state.trigger.setAttribute('aria-activedescendant', activeItem.id);
    } else {
      state.trigger.removeAttribute('aria-activedescendant');
    }
  };

  const refreshDropdownMenu = (root) => {
    const state = states.get(root);
    if (!state) return;

    const elements = getElements(root);
    if (!elements.trigger || !elements.popover || !elements.menu) {
      const missing = [];
      if (!elements.trigger) missing.push('trigger');
      if (!elements.popover) missing.push('popover');
      if (!elements.menu) missing.push('menu');
      console.error(`Dropdown menu refresh failed. Missing element(s): ${missing.join(', ')}`, root);
      return;
    }

    Object.assign(state, elements);
    state.items = getItems(state.menu);
    if (state.activeIndex > -1 && !state.items[state.activeIndex]) setActiveItem(state, -1);
  };

  const closePopover = (state, focusOnTrigger = true) => {
    if (state.trigger.getAttribute('aria-expanded') === 'false') return;
    state.trigger.setAttribute('aria-expanded', 'false');
    state.trigger.removeAttribute('aria-activedescendant');
    state.popover.setAttribute('aria-hidden', 'true');
    if (focusOnTrigger) state.trigger.focus();
    setActiveItem(state, -1);
  };

  const openPopover = (root, state, initialSelection = false) => {
    document.dispatchEvent(new CustomEvent('basecoat:popover', { detail: { source: root } }));
    root.refresh();
    state.trigger.setAttribute('aria-expanded', 'true');
    state.popover.setAttribute('aria-hidden', 'false');

    if (state.items.length > 0 && initialSelection) {
      setActiveItem(state, initialSelection === 'last' ? state.items.length - 1 : 0);
    }
  };

  const initDropdownMenu = (root) => {
    if (root.dataset.dropdownMenuInitialized) return;

    const state = { activeIndex: -1, items: [] };
    states.set(root, state);
    root.refresh = () => refreshDropdownMenu(root);

    refreshDropdownMenu(root);
    if (!state.trigger || !state.popover || !state.menu) {
      states.delete(root);
      delete root.refresh;
      return;
    }

    root.open = (initialSelection = false) => openPopover(root, state, initialSelection);
    root.close = (focusOnTrigger = true) => closePopover(state, focusOnTrigger);
    root.toggle = () => state.trigger.getAttribute('aria-expanded') === 'true' ? root.close() : root.open(false);

    const handleTriggerClick = root.toggle;

    const handleKeydown = (event) => {
      const isExpanded = state.trigger.getAttribute('aria-expanded') === 'true';

      if (event.key === 'Escape') {
        if (isExpanded) root.close();
        return;
      }

      if (!isExpanded) {
        if (['Enter', ' '].includes(event.key)) {
          event.preventDefault();
          root.open(false);
        } else if (event.key === 'ArrowDown') {
          event.preventDefault();
          root.open('first');
        } else if (event.key === 'ArrowUp') {
          event.preventDefault();
          root.open('last');
        }
        return;
      }

      if (state.items.length === 0) return;

      let nextIndex = state.activeIndex;
      if (event.key === 'ArrowDown') {
        event.preventDefault();
        nextIndex = state.activeIndex === -1 ? 0 : Math.min(state.activeIndex + 1, state.items.length - 1);
      } else if (event.key === 'ArrowUp') {
        event.preventDefault();
        nextIndex = state.activeIndex === -1 ? state.items.length - 1 : Math.max(state.activeIndex - 1, 0);
      } else if (event.key === 'Home') {
        event.preventDefault();
        nextIndex = 0;
      } else if (event.key === 'End') {
        event.preventDefault();
        nextIndex = state.items.length - 1;
      } else if (['Enter', ' '].includes(event.key)) {
        event.preventDefault();
        state.items[state.activeIndex]?.click();
        root.close();
        return;
      } else {
        return;
      }

      if (nextIndex !== state.activeIndex) setActiveItem(state, nextIndex);
    };

    const handleMenuMousemove = (event) => {
      const menuItem = event.target.closest('[role^="menuitem"]');
      if (menuItem && !isDisabled(menuItem) && state.items.includes(menuItem)) {
        const index = state.items.indexOf(menuItem);
        if (index !== state.activeIndex) setActiveItem(state, index);
      }
    };

    const handleMenuMouseleave = () => setActiveItem(state, -1);
    const handleMenuClick = (event) => {
      const menuItem = event.target.closest('[role^="menuitem"]');
      if (!menuItem || isDisabled(menuItem)) return;

      if (menuItem.getAttribute('role') === 'menuitemcheckbox') {
        menuItem.setAttribute('aria-checked', menuItem.getAttribute('aria-checked') !== 'true');
      } else if (menuItem.getAttribute('role') === 'menuitemradio') {
        const group = menuItem.closest('[role="group"], [role="menu"]');
        group?.querySelectorAll('[role="menuitemradio"]').forEach((item) => {
          if (!isDisabled(item)) item.setAttribute('aria-checked', item === menuItem ? 'true' : 'false');
        });
      }

      root.close();
    };

    const handleDocumentClick = (event) => {
      if (!root.contains(event.target)) root.close(false);
    };

    const handleDocumentPopover = (event) => {
      if (event.detail.source !== root) root.close(false);
    };

    state.trigger.addEventListener('click', handleTriggerClick);
    root.addEventListener('keydown', handleKeydown);
    state.menu.addEventListener('mousemove', handleMenuMousemove);
    state.menu.addEventListener('mouseleave', handleMenuMouseleave);
    state.menu.addEventListener('click', handleMenuClick);
    document.addEventListener('click', handleDocumentClick);
    document.addEventListener('basecoat:popover', handleDocumentPopover);

    root._destroy = () => {
      state.trigger.removeEventListener('click', handleTriggerClick);
      root.removeEventListener('keydown', handleKeydown);
      state.menu.removeEventListener('mousemove', handleMenuMousemove);
      state.menu.removeEventListener('mouseleave', handleMenuMouseleave);
      state.menu.removeEventListener('click', handleMenuClick);
      document.removeEventListener('click', handleDocumentClick);
      document.removeEventListener('basecoat:popover', handleDocumentPopover);
      states.delete(root);
      delete root.refresh;
      delete root.open;
      delete root.close;
      delete root.toggle;
    };

    state.trigger.setAttribute('aria-expanded', 'false');
    state.popover.setAttribute('aria-hidden', 'true');
    root.dataset.dropdownMenuInitialized = 'true';
    root.dispatchEvent(new CustomEvent('basecoat:initialized'));
  };

  if (window.basecoat) {
    window.basecoat.register('dropdown-menu', {
      selector: '.dropdown-menu:not([data-dropdown-menu-initialized])',
      init: initDropdownMenu,
      refresh: refreshDropdownMenu,
    });
  }
})();

/* --- popover --- */
(() => {
  const initPopover = (popoverComponent) => {
    if (popoverComponent.dataset.popoverInitialized) return;

    const trigger = popoverComponent.querySelector(':scope > button');
    const content = popoverComponent.querySelector(':scope > [data-popover]');

    if (!trigger || !content) {
      const missing = [];
      if (!trigger) missing.push('trigger');
      if (!content) missing.push('content');
      console.error(`Popover initialisation failed. Missing element(s): ${missing.join(', ')}`, popoverComponent);
      return;
    }

    const closePopover = (focusOnTrigger = true) => {
      if (trigger.getAttribute('aria-expanded') === 'false') return;
      trigger.setAttribute('aria-expanded', 'false');
      content.setAttribute('aria-hidden', 'true');
      if (focusOnTrigger) {
        trigger.focus();
      }
    };

    const openPopover = () => {
      document.dispatchEvent(new CustomEvent('basecoat:popover', {
        detail: { source: popoverComponent }
      }));
      
      const elementToFocus = content.querySelector('[autofocus]');
      if (elementToFocus) {
        content.addEventListener('transitionend', () => {
          elementToFocus.focus();
        }, { once: true });
      }

      trigger.setAttribute('aria-expanded', 'true');
      content.setAttribute('aria-hidden', 'false');
    };

    const handleTriggerClick = () => {
      const isExpanded = trigger.getAttribute('aria-expanded') === 'true';
      if (isExpanded) {
        closePopover();
      } else {
        openPopover();
      }
    };

    const handleKeydown = (event) => {
      if (event.key === 'Escape') {
        closePopover();
      }
    };

    const handleDocumentClick = (event) => {
      if (!popoverComponent.contains(event.target)) {
        closePopover();
      }
    };

    const handleDocumentPopover = (event) => {
      if (event.detail.source !== popoverComponent) {
        closePopover(false);
      }
    };

    trigger.addEventListener('click', handleTriggerClick);
    popoverComponent.addEventListener('keydown', handleKeydown);
    document.addEventListener('click', handleDocumentClick);
    document.addEventListener('basecoat:popover', handleDocumentPopover);

    popoverComponent._destroy = () => {
      trigger.removeEventListener('click', handleTriggerClick);
      popoverComponent.removeEventListener('keydown', handleKeydown);
      document.removeEventListener('click', handleDocumentClick);
      document.removeEventListener('basecoat:popover', handleDocumentPopover);
    };

    popoverComponent.dataset.popoverInitialized = true;
    popoverComponent.dispatchEvent(new CustomEvent('basecoat:initialized'));
  };

  if (window.basecoat) {
    window.basecoat.register('popover', '.popover:not([data-popover-initialized])', initPopover);
  }
})();

/* --- select --- */
(() => {
  const states = new WeakMap();

  const getElements = (root) => {
    const trigger = root.querySelector(':scope > button');
    const selectedLabel = trigger?.querySelector(':scope > span') || null;
    const popover = root.querySelector(':scope > [data-popover]');
    const listbox = popover ? popover.querySelector('[role="listbox"]') : null;
    const input = root.querySelector(':scope > input[type="hidden"]');
    return { trigger, selectedLabel, popover, listbox, input };
  };

  const getValue = (option) => option.dataset.value ?? option.textContent.trim();
  const getLabel = (option) => option.dataset.label || option.textContent.trim();
  const getFormat = (root) => root.dataset.format === 'object' ? 'object' : 'value';
  const isDisabled = (option) => option.getAttribute('aria-disabled') === 'true';
  const toSelected = (option) => ({ value: getValue(option), label: getLabel(option) });

  const getOptions = (listbox) => {
    const allOptions = Array.from(listbox.querySelectorAll('[role="option"]'));
    return {
      allOptions,
      options: allOptions.filter(option => !isDisabled(option)),
    };
  };

  const parseStoredValues = (storedValue, { isMultiple, format }) => {
    if (isMultiple) {
      try {
        const parsed = JSON.parse(storedValue || '[]');
        if (!Array.isArray(parsed)) return [];
        return parsed
          .map(item => format === 'object' && item && typeof item === 'object' ? item.value : item)
          .filter(value => value != null)
          .map(String);
      } catch (_) {
        return [];
      }
    }

    if (format === 'object') {
      try {
        const parsed = JSON.parse(storedValue || 'null');
        return parsed && typeof parsed === 'object' && parsed.value != null ? String(parsed.value) : '';
      } catch (_) {
        return '';
      }
    }

    return storedValue || '';
  };

  const serializeSelection = (state, selected) => {
    if (state.format === 'object') {
      return JSON.stringify(state.isMultiple ? selected : (selected[0] || null));
    }

    const value = selected.map(item => item.value);
    return state.isMultiple ? JSON.stringify(value) : (value[0] || '');
  };

  const showPlaceholder = (state) => {
    state.selectedLabel.textContent = state.placeholder || '';
    state.selectedLabel.classList.toggle('text-muted-foreground', Boolean(state.placeholder));
    state.input.value = state.isMultiple ? serializeSelection(state, []) : '';
  };

  const scrollOptionIntoListbox = (state, option) => {
    const optionRect = option.getBoundingClientRect();
    const listboxRect = state.listbox.getBoundingClientRect();

    if (optionRect.top < listboxRect.top) {
      state.listbox.scrollTop -= listboxRect.top - optionRect.top;
    } else if (optionRect.bottom > listboxRect.bottom) {
      state.listbox.scrollTop += optionRect.bottom - listboxRect.bottom;
    }
  };

  const setActiveOption = (state, index) => {
    if (state.activeIndex > -1 && state.options[state.activeIndex]) {
      state.options[state.activeIndex].classList.remove('active');
    }

    state.activeIndex = index;

    if (state.activeIndex > -1) {
      const activeOption = state.options[state.activeIndex];
      activeOption.classList.add('active');
      if (activeOption.id) {
        state.trigger.setAttribute('aria-activedescendant', activeOption.id);
      } else {
        state.trigger.removeAttribute('aria-activedescendant');
      }
    } else {
      state.trigger.removeAttribute('aria-activedescendant');
    }
  };

  const updateValue = (root, optionOrOptions, triggerEvent = true) => {
    const state = states.get(root);
    let value;
    let selectedDetail;

    if (state.isMultiple) {
      const selected = Array.isArray(optionOrOptions) ? optionOrOptions : [];
      state.selectedOptions.clear();
      selected.forEach(option => state.selectedOptions.add(option));

      const selectedInOrder = state.options.filter(option => state.selectedOptions.has(option));
      selectedDetail = selectedInOrder.map(toSelected);
      if (selectedInOrder.length === 0) {
        state.selectedLabel.textContent = state.placeholder;
        state.selectedLabel.classList.add('text-muted-foreground');
      } else {
        state.selectedLabel.textContent = selectedDetail.map(item => item.label).join(', ');
        state.selectedLabel.classList.remove('text-muted-foreground');
      }

      value = selectedDetail.map(item => item.value);
      state.input.value = serializeSelection(state, selectedDetail);
    } else {
      const option = optionOrOptions;
      if (!option) {
        state.options.forEach(option => option.removeAttribute('aria-selected'));
        showPlaceholder(state);
        selectedDetail = null;
        value = '';
      } else {
        if (option.dataset.label) {
          state.selectedLabel.textContent = option.dataset.label;
        } else {
          state.selectedLabel.innerHTML = option.innerHTML;
        }
        state.selectedLabel.classList.remove('text-muted-foreground');
        selectedDetail = toSelected(option);
        value = selectedDetail.value;
        state.input.value = serializeSelection(state, [selectedDetail]);
      }
    }

    state.options.forEach(option => {
      const isSelected = state.isMultiple ? state.selectedOptions.has(option) : optionOrOptions && option === optionOrOptions;
      if (isSelected) {
        option.setAttribute('aria-selected', 'true');
      } else {
        option.removeAttribute('aria-selected');
      }
    });

    if (triggerEvent) {
      root.dispatchEvent(new CustomEvent('change', {
        detail: { value, selected: selectedDetail },
        bubbles: true,
      }));
    }
  };

  const closePopover = (state, focusOnTrigger = true) => {
    if (state.popover.getAttribute('aria-hidden') === 'true') return;
    if (focusOnTrigger) state.trigger.focus();
    state.popover.setAttribute('aria-hidden', 'true');
    state.trigger.setAttribute('aria-expanded', 'false');
    setActiveOption(state, -1);
  };

  const refreshSelect = (root) => {
    const state = states.get(root);
    if (!state) return;

    const elements = getElements(root);
    if (!elements.trigger || !elements.selectedLabel || !elements.popover || !elements.listbox || !elements.input) {
      const missing = [];
      if (!elements.trigger) missing.push('trigger');
      if (!elements.selectedLabel) missing.push('selected label');
      if (!elements.popover) missing.push('popover');
      if (!elements.listbox) missing.push('listbox');
      if (!elements.input) missing.push('input');
      console.error(`Select component refresh failed. Missing element(s): ${missing.join(', ')}`, root);
      return;
    }

    const previousValue = elements.input.value;
    Object.assign(state, elements, getOptions(elements.listbox));
    state.visibleOptions = [...state.options];
    state.isMultiple = state.listbox.getAttribute('aria-multiselectable') === 'true';
    state.format = getFormat(root);
    state.placeholder = root.dataset.placeholder || '';
    state.closeOnSelect = root.dataset.closeOnSelect === 'true';

    if (state.isMultiple) {
      if (!state.selectedOptions) state.selectedOptions = new Set();
      const values = parseStoredValues(previousValue, state);
      const selected = values.length
        ? values.map(value => state.options.find(option => getValue(option) === value)).filter(Boolean)
        : state.options.filter(option => option.getAttribute('aria-selected') === 'true');
      updateValue(root, selected, false);
    } else {
      const value = parseStoredValues(previousValue, state);
      const selected = value === '' && state.placeholder
        ? null
        : state.options.find(option => getValue(option) === value)
        || state.options.find(option => option.getAttribute('aria-selected') === 'true');
      state.options.forEach(option => option.removeAttribute('aria-selected'));
      updateValue(root, selected || null, false);
    }

    const selectedOption = state.listbox.querySelector('[role="option"][aria-selected="true"]');
    setActiveOption(state, selectedOption ? state.options.indexOf(selectedOption) : -1);
  };

  const toggleMultipleValue = (root, option) => {
    const state = states.get(root);
    if (state.selectedOptions.has(option)) {
      state.selectedOptions.delete(option);
    } else {
      state.selectedOptions.add(option);
    }
    updateValue(root, state.options.filter(opt => state.selectedOptions.has(opt)));
  };

  const selectValue = (root, value) => {
    const state = states.get(root);
    if (state.isMultiple) {
      const option = state.options.find(opt => getValue(opt) === value && !state.selectedOptions.has(opt));
      if (!option) return;
      state.selectedOptions.add(option);
      updateValue(root, state.options.filter(opt => state.selectedOptions.has(opt)));
    } else {
      const option = state.options.find(opt => getValue(opt) === value);
      if (!option) return;
      if (state.placeholder && getValue(option) === '') {
        updateValue(root, null);
        closePopover(state);
        return;
      }
      if (root.value !== value) updateValue(root, option);
      closePopover(state);
    }
  };

  const deselectValue = (root, value) => {
    const state = states.get(root);
    if (!state.isMultiple) return;
    const option = state.options.find(opt => getValue(opt) === value && state.selectedOptions.has(opt));
    if (!option) return;
    state.selectedOptions.delete(option);
    updateValue(root, state.options.filter(opt => state.selectedOptions.has(opt)));
  };

  const handleKeyNavigation = (event, root) => {
    const state = states.get(root);
    const isPopoverOpen = state.popover.getAttribute('aria-hidden') === 'false';

    if (!['ArrowDown', 'ArrowUp', 'Enter', 'Home', 'End', 'Escape'].includes(event.key)) return;

    if (!isPopoverOpen) {
      if (event.key !== 'Enter' && event.key !== 'Escape') {
        event.preventDefault();
        root.open();
      }
      return;
    }

    event.preventDefault();

    if (event.key === 'Escape') {
      root.close();
      return;
    }

    if (event.key === 'Enter') {
      if (state.activeIndex > -1) {
        const option = state.options[state.activeIndex];
        if (state.isMultiple) {
          toggleMultipleValue(root, option);
          if (state.closeOnSelect) root.close();
        } else {
          if (state.placeholder && getValue(option) === '') {
            updateValue(root, null);
          } else if (root.value !== getValue(option)) {
            updateValue(root, option);
          }
          root.close();
        }
      }
      return;
    }

    if (state.visibleOptions.length === 0) return;

    const currentVisibleIndex = state.activeIndex > -1 ? state.visibleOptions.indexOf(state.options[state.activeIndex]) : -1;
    let nextVisibleIndex = currentVisibleIndex;

    if (event.key === 'ArrowDown' && currentVisibleIndex < state.visibleOptions.length - 1) nextVisibleIndex = currentVisibleIndex + 1;
    if (event.key === 'ArrowUp') nextVisibleIndex = currentVisibleIndex > 0 ? currentVisibleIndex - 1 : 0;
    if (event.key === 'Home') nextVisibleIndex = 0;
    if (event.key === 'End') nextVisibleIndex = state.visibleOptions.length - 1;

    if (nextVisibleIndex !== currentVisibleIndex) {
      const newActiveOption = state.visibleOptions[nextVisibleIndex];
      setActiveOption(state, state.options.indexOf(newActiveOption));
      scrollOptionIntoListbox(state, newActiveOption);
    }
  };

  const initSelect = (root) => {
    if (root.dataset.selectInitialized) return;

    const state = { activeIndex: -1, selectedOptions: null, options: [], allOptions: [], visibleOptions: [], format: 'value' };
    states.set(root, state);
    root.refresh = () => refreshSelect(root);

    refreshSelect(root);
    if (!state.trigger || !state.selectedLabel || !state.popover || !state.listbox || !state.input) {
      states.delete(root);
      delete root.refresh;
      return;
    }

    root.open = () => {
      document.dispatchEvent(new CustomEvent('basecoat:popover', { detail: { source: root } }));
      root.refresh();
      state.popover.setAttribute('aria-hidden', 'false');
      state.trigger.setAttribute('aria-expanded', 'true');

      const selectedOption = state.listbox.querySelector('[role="option"][aria-selected="true"]');
      if (selectedOption) {
        setActiveOption(state, state.options.indexOf(selectedOption));
        scrollOptionIntoListbox(state, selectedOption);
      }
    };
    root.close = (focusOnTrigger = true) => closePopover(state, focusOnTrigger);
    root.togglePopover = () => state.trigger.getAttribute('aria-expanded') === 'true' ? root.close() : root.open();

    const handleTriggerKeydown = (event) => handleKeyNavigation(event, root);
    const handleTriggerClick = root.togglePopover;
    const handleListboxMousemove = (event) => {
      const option = event.target.closest('[role="option"]');
      if (option && state.visibleOptions.includes(option)) {
        const index = state.options.indexOf(option);
        if (index !== state.activeIndex) setActiveOption(state, index);
      }
    };
    const handleListboxMouseleave = () => {
      const selectedOption = state.listbox.querySelector('[role="option"][aria-selected="true"]');
      setActiveOption(state, selectedOption ? state.options.indexOf(selectedOption) : -1);
    };
    const handleListboxClick = (event) => {
      const clickedOption = event.target.closest('[role="option"]');
      if (!clickedOption) return;

      const option = state.options.find(opt => opt === clickedOption);
      if (!option) return;

      if (state.isMultiple) {
        toggleMultipleValue(root, option);
        if (state.closeOnSelect) {
          root.close();
        } else {
          setActiveOption(state, state.options.indexOf(option));
          state.trigger.focus();
        }
      } else {
        if (state.placeholder && getValue(option) === '') {
          updateValue(root, null);
        } else if (root.value !== getValue(option)) {
          updateValue(root, option);
        }
        root.close();
      }
    };
    const handleDocumentClick = (event) => {
      if (!root.contains(event.target)) root.close(false);
    };
    const handleDocumentPopover = (event) => {
      if (event.detail.source !== root) root.close(false);
    };

    state.trigger.addEventListener('keydown', handleTriggerKeydown);
    state.trigger.addEventListener('click', handleTriggerClick);
    state.listbox.addEventListener('mousemove', handleListboxMousemove);
    state.listbox.addEventListener('mouseleave', handleListboxMouseleave);
    state.listbox.addEventListener('click', handleListboxClick);
    document.addEventListener('click', handleDocumentClick);
    document.addEventListener('basecoat:popover', handleDocumentPopover);

    root._destroy = () => {
      state.trigger.removeEventListener('keydown', handleTriggerKeydown);
      state.trigger.removeEventListener('click', handleTriggerClick);
      state.listbox.removeEventListener('mousemove', handleListboxMousemove);
      state.listbox.removeEventListener('mouseleave', handleListboxMouseleave);
      state.listbox.removeEventListener('click', handleListboxClick);
      document.removeEventListener('click', handleDocumentClick);
      document.removeEventListener('basecoat:popover', handleDocumentPopover);
      states.delete(root);
      delete root.refresh;
      delete root.open;
      delete root.close;
      delete root.togglePopover;
      delete root.select;
      delete root.selectByValue;
      delete root.deselect;
      delete root.toggle;
      delete root.selectAll;
      delete root.selectNone;
    };

    Object.defineProperty(root, 'value', {
      configurable: true,
      get: () => state.isMultiple ? state.options.filter(option => state.selectedOptions.has(option)).map(getValue) : parseStoredValues(state.input.value, state),
      set: (value) => {
        if (state.isMultiple) {
          const values = Array.isArray(value) ? value : (value != null ? [value] : []);
          updateValue(root, values.map(v => state.options.find(option => getValue(option) === v)).filter(Boolean));
        } else {
          if (value == null || value === '') {
            updateValue(root, null);
            root.close();
            return;
          }
          const option = state.options.find(opt => getValue(opt) === value);
          if (option) {
            updateValue(root, option);
            root.close();
          }
        }
      },
    });

    Object.defineProperty(root, 'selected', {
      configurable: true,
      get: () => {
        if (state.isMultiple) return state.options.filter(option => state.selectedOptions.has(option)).map(toSelected);
        const value = root.value;
        const option = state.options.find(opt => getValue(opt) === value);
        return option ? toSelected(option) : null;
      },
    });

    root.select = (value) => selectValue(root, value);
    root.selectByValue = root.select;
    if (state.isMultiple) {
      root.deselect = (value) => deselectValue(root, value);
      root.toggle = (value) => {
        const option = state.options.find(opt => getValue(opt) === value);
        if (option) toggleMultipleValue(root, option);
      };
      root.selectAll = () => updateValue(root, state.options);
      root.selectNone = () => updateValue(root, []);
    }

    state.popover.setAttribute('aria-hidden', 'true');
    state.trigger.setAttribute('aria-expanded', 'false');
    root.dataset.selectInitialized = 'true';
    root.dispatchEvent(new CustomEvent('basecoat:initialized'));
  };

  if (window.basecoat) {
    window.basecoat.register('select', {
      selector: 'div.select:not([data-select-initialized])',
      init: initSelect,
      refresh: refreshSelect,
    });
  }
})();

/* --- sidebar --- */
(() => {
  const initSidebar = (sidebarComponent) => {
    if (sidebarComponent.dataset.sidebarInitialized && typeof sidebarComponent.toggle === 'function') return;

    const initialOpen = sidebarComponent.dataset.initialOpen !== 'false';
    const initialMobileOpen = sidebarComponent.dataset.initialMobileOpen === 'true';
    const breakpoint = parseInt(sidebarComponent.dataset.breakpoint) || 768;

    let open = breakpoint > 0
      ? (window.innerWidth >= breakpoint ? initialOpen : initialMobileOpen)
      : initialOpen;

    const updateState = () => {
      sidebarComponent.setAttribute('aria-hidden', String(!open));
      if (open) {
        sidebarComponent.removeAttribute('inert');
      } else {
        sidebarComponent.setAttribute('inert', '');
      }
    };

    const setState = (state) => {
      open = Boolean(state);
      updateState();
    };

    sidebarComponent.open = () => setState(true);
    sidebarComponent.close = () => setState(false);
    sidebarComponent.toggle = () => setState(!open);

    const handleClick = (event) => {
      const target = event.target;
      const nav = sidebarComponent.querySelector('nav');
      const isMobile = window.innerWidth < breakpoint;

      if (isMobile && target.closest('a, button') && !target.closest('[data-keep-mobile-sidebar-open]')) {
        if (document.activeElement) document.activeElement.blur();
        sidebarComponent.close();
        return;
      }

      if (target === sidebarComponent || (nav && !nav.contains(target))) {
        if (document.activeElement) document.activeElement.blur();
        sidebarComponent.close();
      }
    };

    sidebarComponent.addEventListener('click', handleClick);

    sidebarComponent._destroy = () => {
      sidebarComponent.removeEventListener('click', handleClick);
      delete sidebarComponent.open;
      delete sidebarComponent.close;
      delete sidebarComponent.toggle;
    };

    updateState();
    sidebarComponent.dataset.sidebarInitialized = 'true';
    sidebarComponent.dispatchEvent(new CustomEvent('basecoat:initialized'));
  };

  if (window.basecoat) {
    window.basecoat.register('sidebar', '.sidebar', initSidebar);
  }
})();

/* --- tabs --- */
(() => {
  const states = new WeakMap();

  const isDisabled = (tab) => tab.disabled || tab.getAttribute('aria-disabled') === 'true';

  const getElements = (root) => {
    const tablist = root.querySelector('[role="tablist"]');
    const tabs = tablist ? Array.from(tablist.querySelectorAll('[role="tab"]')) : [];
    const panels = tabs.map(tab => document.getElementById(tab.getAttribute('aria-controls'))).filter(Boolean);
    return { tablist, tabs, panels };
  };

  const refreshTabs = (root) => {
    const state = states.get(root);
    if (!state) return;

    Object.assign(state, getElements(root));
    if (!state.tablist) return;

    const selected = state.tabs.find(tab => tab.getAttribute('aria-selected') === 'true' && !isDisabled(tab))
      || state.tabs.find(tab => !isDisabled(tab));
    if (selected) root.select(selected, false);
  };

  const initTabs = (root) => {
    if (root.dataset.tabsInitialized) return;

    const state = {};
    states.set(root, state);
    root.refresh = () => refreshTabs(root);

    const selectTab = (tabToSelect, focus = false) => {
      if (!tabToSelect || isDisabled(tabToSelect)) return;

      state.tabs.forEach((tab) => {
        tab.setAttribute('aria-selected', 'false');
        tab.setAttribute('tabindex', '-1');
        const panel = document.getElementById(tab.getAttribute('aria-controls'));
        if (panel) panel.hidden = true;
      });

      tabToSelect.setAttribute('aria-selected', 'true');
      tabToSelect.setAttribute('tabindex', '0');
      const activePanel = document.getElementById(tabToSelect.getAttribute('aria-controls'));
      if (activePanel) activePanel.hidden = false;
      if (focus) tabToSelect.focus();
    };

    root.select = selectTab;
    refreshTabs(root);
    if (!state.tablist) {
      states.delete(root);
      delete root.refresh;
      delete root.select;
      return;
    }

    const handleClick = (event) => {
      const clickedTab = event.target.closest('[role="tab"]');
      if (clickedTab) root.select(clickedTab);
    };

    const handleKeydown = (event) => {
      const currentTab = event.target;
      if (!state.tabs.includes(currentTab)) return;

      const enabledTabs = state.tabs.filter(tab => !isDisabled(tab));
      const currentIndex = enabledTabs.indexOf(currentTab);
      const orientation = state.tablist.getAttribute('aria-orientation') || 'horizontal';
      if (currentIndex === -1) return;

      let nextTab;
      if (event.key === 'ArrowRight' && orientation === 'horizontal') nextTab = enabledTabs[(currentIndex + 1) % enabledTabs.length];
      if (event.key === 'ArrowLeft' && orientation === 'horizontal') nextTab = enabledTabs[(currentIndex - 1 + enabledTabs.length) % enabledTabs.length];
      if (event.key === 'ArrowDown' && orientation === 'vertical') nextTab = enabledTabs[(currentIndex + 1) % enabledTabs.length];
      if (event.key === 'ArrowUp' && orientation === 'vertical') nextTab = enabledTabs[(currentIndex - 1 + enabledTabs.length) % enabledTabs.length];
      if (event.key === 'Home') nextTab = enabledTabs[0];
      if (event.key === 'End') nextTab = enabledTabs[enabledTabs.length - 1];
      if (!nextTab) return;

      event.preventDefault();
      root.select(nextTab, true);
    };

    state.tablist.addEventListener('click', handleClick);
    state.tablist.addEventListener('keydown', handleKeydown);

    root._destroy = () => {
      state.tablist.removeEventListener('click', handleClick);
      state.tablist.removeEventListener('keydown', handleKeydown);
      states.delete(root);
      delete root.refresh;
      delete root.select;
    };

    root.dataset.tabsInitialized = 'true';
    root.dispatchEvent(new CustomEvent('basecoat:initialized'));
  };

  if (window.basecoat) {
    window.basecoat.register('tabs', {
      selector: '.tabs:not([data-tabs-initialized])',
      init: initTabs,
      refresh: refreshTabs,
    });
  }
})();

/* --- toast --- */
(() => {
  let toaster;
  const toasts = new WeakMap();
  let isPaused = false;
  const ICONS = {
    success: '<svg aria-hidden="true" xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><path d="m9 12 2 2 4-4"/></svg>',
    error: '<svg aria-hidden="true" xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><path d="m15 9-6 6"/><path d="m9 9 6 6"/></svg>',
    info: '<svg aria-hidden="true" xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><path d="M12 16v-4"/><path d="M12 8h.01"/></svg>',
    warning: '<svg aria-hidden="true" xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="m21.73 18-8-14a2 2 0 0 0-3.48 0l-8 14A2 2 0 0 0 4 21h16a2 2 0 0 0 1.73-3"/><path d="M12 9v4"/><path d="M12 17h.01"/></svg>'
  };

  function initToaster(toasterElement) {
    if (toasterElement.dataset.toasterInitialized) return;
    toaster = toasterElement;

    const handleClick = (event) => {
      const actionLink = event.target.closest('.toast footer a');
      const actionButton = event.target.closest('.toast footer button');
      if (actionLink || actionButton) {
        closeToast(event.target.closest('.toast'));
      }
    };

    toaster.addEventListener('mouseenter', pauseAllTimeouts);
    toaster.addEventListener('mouseleave', resumeAllTimeouts);
    toaster.addEventListener('click', handleClick);

    toasterElement.toast = (config = {}) => {
      const toastElement = createToast(config);
      toasterElement.appendChild(toastElement);
      initToast(toastElement);
      return toastElement;
    };
    toasterElement.closeAll = () => {
      toasterElement.querySelectorAll('.toast:not([aria-hidden="true"])').forEach(closeToast);
    };

    toaster.querySelectorAll('.toast:not([data-toast-initialized])').forEach(initToast);
    toaster._destroy = () => {
      toaster.removeEventListener('mouseenter', pauseAllTimeouts);
      toaster.removeEventListener('mouseleave', resumeAllTimeouts);
      toaster.removeEventListener('click', handleClick);
      toaster.querySelectorAll('.toast[data-toast-initialized]').forEach(toast => toast._destroy?.());
      delete toaster.toast;
      delete toaster.closeAll;
      if (toaster === toasterElement) toaster = null;
    };
    toaster.dataset.toasterInitialized = 'true';
    toaster.dispatchEvent(new CustomEvent('basecoat:initialized'));
  }

  function initToast(element) {
    if (element.dataset.toastInitialized) return;

    const duration = parseInt(element.dataset.duration);
    const timeoutDuration = duration !== -1
      ? duration || (element.dataset.category === 'error' ? 5000 : 3000)
      : -1;

    const state = {
      remainingTime: timeoutDuration,
      timeoutId: null,
      startTime: null,
    };

    if (timeoutDuration !== -1) {
      if (isPaused) {
        state.timeoutId = null;
      } else {
        state.startTime = Date.now();
        state.timeoutId = setTimeout(() => closeToast(element), timeoutDuration);
      }
    }
    toasts.set(element, state);

    element.close = () => closeToast(element);
    element._destroy = () => {
      clearTimeout(state.timeoutId);
      toasts.delete(element);
      delete element.close;
    };
    element.dataset.toastInitialized = 'true';
  }

  function pauseAllTimeouts() {
    if (isPaused || !toaster) return;

    isPaused = true;

    toaster.querySelectorAll('.toast:not([aria-hidden="true"])').forEach(element => {
      if (!toasts.has(element)) return;

      const state = toasts.get(element);
      if (state.timeoutId) {
        clearTimeout(state.timeoutId);
        state.timeoutId = null;
        state.remainingTime -= Date.now() - state.startTime;
      }
    });
  }

  function resumeAllTimeouts() {
    if (!isPaused || !toaster) return;

    isPaused = false;

    toaster.querySelectorAll('.toast:not([aria-hidden="true"])').forEach(element => {
      if (!toasts.has(element)) return;

      const state = toasts.get(element);
      if (state.remainingTime !== -1 && !state.timeoutId) {
        if (state.remainingTime > 0) {
          state.startTime = Date.now();
          state.timeoutId = setTimeout(() => closeToast(element), state.remainingTime);
        } else {
          closeToast(element);
        }
      }
    });
  }

  function closeToast(element) {
    if (!element || !toasts.has(element)) return;

    const state = toasts.get(element);
    clearTimeout(state.timeoutId);
    toasts.delete(element);

    if (element.contains(document.activeElement)) document.activeElement.blur();
    element.setAttribute('aria-hidden', 'true');
    element.addEventListener('transitionend', () => element.remove(), { once: true });
  }

  function createToast(config) {
    const {
      category = 'info',
      title,
      description,
      action,
      cancel,
      duration,
      icon,
    } = config;

    const iconHtml = icon || (category && ICONS[category]) || '';
    const titleHtml = title ? `<h2>${title}</h2>` : '';
    const descriptionHtml = description ? `<p>${description}</p>` : '';
    const actionHtml = action?.href
      ? `<a href="${action.href}" class="btn" data-toast-action>${action.label}</a>`
      : action?.onclick
        ? `<button type="button" class="btn" data-toast-action onclick="${action.onclick}">${action.label}</button>`
        : '';
    const cancelHtml = cancel
      ? `<button type="button" class="btn h-6 text-xs px-2.5 rounded-sm" data-variant="outline" data-toast-cancel onclick="${cancel?.onclick || ''}">${cancel.label}</button>`
      : '';

    const footerHtml = actionHtml || cancelHtml ? `<footer>${actionHtml}${cancelHtml}</footer>` : '';

    const html = `
      <div
        class="toast"
        role="${category === 'error' ? 'alert' : 'status'}"
        aria-atomic="true"
        ${category ? `data-category="${category}"` : ''}
        ${duration !== undefined ? `data-duration="${duration}"` : ''}
      >
        <div class="toast-content">
          ${iconHtml}
          <section>
            ${titleHtml}
            ${descriptionHtml}
          </section>
          ${footerHtml}
        </div>
      </div>
    `;
    const template = document.createElement('template');
    template.innerHTML = html.trim();
    return template.content.firstChild;
  }

  if (window.basecoat) {
    window.basecoat.register('toaster', '#toaster:not([data-toaster-initialized])', initToaster);
    window.basecoat.register('toast', '.toast:not([data-toast-initialized])', initToast);
  }
})();
