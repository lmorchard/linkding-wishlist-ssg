// Wishlist interactive views: Table / Grid switch, client-side search, sort, and filter.
(function() {
  const dataScript = document.getElementById('wishlist-data');
  if (!dataScript) return;

  let items = [];
  try {
    items = JSON.parse(dataScript.textContent);
  } catch (e) {
    console.error('Failed to parse wishlist data', e);
    return;
  }

  const container = document.getElementById('wishlist-app');
  if (!container) return;

  // State
  let currentView = localStorage.getItem('wishlist_view') || 'table'; // 'table' or 'grid'
  let query = '';
  let sortBy = 'default'; // 'default', 'price-asc', 'price-desc', 'title', 'store', 'added-asc', 'added-desc'
  let filterStock = 'all'; // 'all', 'instock', 'unavailable'
  let filterPrice = 'all'; // 'all', 'u25', '25-50', '50-100', 'o100', 'unpriced'
  let filterPriority = 'all'; // 'all', 'top', 'p0', 'p1', 'p2', 'p3', 'p4', 'p5'
  let filterYear = 'all'; // 'all', or specific year string like '2026', '2025'
  let selectedTag = ''; // Empty string means all tags
  let currentPage = 1;
  const pageSize = 50;

  // Extract available years sorted descending
  const yearsSet = new Set();
  items.forEach(item => {
    if (item.added_at) {
      const yr = new Date(item.added_at).getFullYear();
      if (!isNaN(yr)) yearsSet.add(yr);
    }
  });
  const sortedYears = Array.from(yearsSet).sort((a, b) => b - a);

  // Count top tags for facet chips
  const tagCounts = {};
  items.forEach(item => {
    if (item.tags) {
      item.tags.forEach(t => {
        const lower = t.toLowerCase();
        // Exclude root selector, provenance tags, and priority tags from topic chips
        if (lower === 'wishlist' || lower === 'amazon' || lower.startsWith('imported:') || /^wishlist:p[0-5]$/.test(lower)) return;
        tagCounts[t] = (tagCounts[t] || 0) + 1;
      });
    }
  });

  const sortedTags = Object.keys(tagCounts)
    .filter(t => tagCounts[t] >= 2) // At least 2 items
    .sort((a, b) => tagCounts[b] - tagCounts[a])
    .slice(0, 15); // Top 15 facets

  // Render controls toolbar
  const toolbar = document.createElement('div');
  toolbar.className = 'wishlist-toolbar';
  toolbar.innerHTML = `
    <div class="toolbar-search">
      <input type="search" id="wishlist-search" placeholder="Search items, notes, stores, tags..." aria-label="Search wishlist">
    </div>
    <div class="toolbar-filters">
      <select id="wishlist-sort" aria-label="Sort by">
        <option value="default">Sort: Priority &amp; Newest</option>
        <option value="added-desc">Date Added: Newest First</option>
        <option value="added-asc">Date Added: Oldest First</option>
        <option value="price-asc">Price: Low to High</option>
        <option value="price-desc">Price: High to Low</option>
        <option value="title">Title: A to Z</option>
        <option value="store">Store</option>
      </select>
      <select id="wishlist-priority" aria-label="Filter priority">
        <option value="all">All Priorities</option>
        <option value="top">Top Wants (P0–P1)</option>
        <option value="p0">P0 Only</option>
        <option value="p1">P1 Only</option>
        <option value="p2">P2 Only</option>
      </select>
      <select id="wishlist-stock" aria-label="Filter availability">
        <option value="all">All Statuses</option>
        <option value="instock">In Stock Only</option>
        <option value="unavailable">Unavailable Only</option>
      </select>
      <select id="wishlist-price" aria-label="Filter by price">
        <option value="all">All Prices</option>
        <option value="u25">Under $25</option>
        <option value="25-50">$25 to $50</option>
        <option value="50-100">$50 to $100</option>
        <option value="o100">Over $100</option>
        <option value="unpriced">Unpriced</option>
      </select>
      <select id="wishlist-year" aria-label="Filter by year">
        <option value="all">All Years</option>
        ${sortedYears.map(yr => `<option value="${yr}">${yr}</option>`).join('')}
      </select>
      <div class="toolbar-view-toggle">
        <button type="button" id="toggle-table" class="${currentView === 'table' ? 'active' : ''}" title="Table View">☷ Table</button>
        <button type="button" id="toggle-grid" class="${currentView === 'grid' ? 'active' : ''}" title="Grid View">⊞ Grid</button>
      </div>
    </div>
  `;

  // Render facet chips bar if tags exist
  const facetBar = document.createElement('div');
  facetBar.className = 'wishlist-facets-bar';
  if (sortedTags.length > 0) {
    let chips = `<button type="button" class="facet-chip ${selectedTag === '' ? 'active' : ''}" data-tag="">All</button>`;
    chips += sortedTags.map(t => {
      const display = t.startsWith('wishlist:') ? t.slice(9) : t;
      return `<button type="button" class="facet-chip ${selectedTag === t ? 'active' : ''}" data-tag="${escapeHTML(t)}">${escapeHTML(display)} <span class="facet-chip-count">${tagCounts[t]}</span></button>`;
    }).join('');
    facetBar.innerHTML = chips;
  }

  // Content container
  const content = document.createElement('div');
  content.className = 'wishlist-interactive-content';

  // Replace server-rendered view with interactive container
  container.innerHTML = '';
  container.appendChild(toolbar);
  if (sortedTags.length > 0) {
    container.appendChild(facetBar);
  }
  container.appendChild(content);

  function escapeHTML(str) {
    if (!str) return '';
    return str.replace(/[&<>'"]/g, tag => ({
      '&': '&amp;',
      '<': '&lt;',
      '>': '&gt;',
      "'": '&#39;',
      '"': '&quot;'
    }[tag] || tag));
  }

  function render() {
    // 1. Filter
    let filtered = items.filter(item => {
      if (filterStock === 'instock' && item.unavailable) return false;
      if (filterStock === 'unavailable' && !item.unavailable) return false;

      // Priority filter
      if (filterPriority !== 'all') {
        const p = item.priority;
        if (filterPriority === 'top') {
          if (p !== 0 && p !== 1) return false;
        } else if (filterPriority === 'p0') {
          if (p !== 0) return false;
        } else if (filterPriority === 'p1') {
          if (p !== 1) return false;
        } else if (filterPriority === 'p2') {
          if (p !== 2) return false;
        }
      }

      // Year filter
      if (filterYear !== 'all') {
        if (!item.added_at) return false;
        const itemYear = new Date(item.added_at).getFullYear().toString();
        if (itemYear !== filterYear) return false;
      }

      // Price bucket filter
      if (filterPrice !== 'all') {
        const p = item.price;
        if (filterPrice === 'unpriced') {
          if (p != null) return false;
        } else if (p == null) {
          return false;
        } else if (filterPrice === 'u25' && p >= 25) {
          return false;
        } else if (filterPrice === '25-50' && (p < 25 || p >= 50)) {
          return false;
        } else if (filterPrice === '50-100' && (p < 50 || p >= 100)) {
          return false;
        } else if (filterPrice === 'o100' && p < 100) {
          return false;
        }
      }

      if (selectedTag && (!item.tags || !item.tags.includes(selectedTag))) return false;
      if (query) {
        const q = query.toLowerCase();
        const inTitle = item.title.toLowerCase().includes(q);
        const inStore = item.store.toLowerCase().includes(q);
        const inDesc = item.description ? item.description.toLowerCase().includes(q) : false;
        const inNotes = item.user_notes ? item.user_notes.toLowerCase().includes(q) : false;
        const inTags = item.tags ? item.tags.some(t => t.toLowerCase().includes(q)) : false;
        if (!inTitle && !inStore && !inDesc && !inNotes && !inTags) return false;
      }
      return true;
    });

    // 2. Sort
    if (sortBy === 'default') {
      filtered.sort((a, b) => {
        const ap = (a.priority !== undefined && a.priority !== null) ? a.priority : 99;
        const bp = (b.priority !== undefined && b.priority !== null) ? b.priority : 99;
        if (ap !== bp) return ap - bp;
        // Same priority: newest added first
        return new Date(b.added_at) - new Date(a.added_at);
      });
    } else if (sortBy === 'added-desc') {
      filtered.sort((a, b) => new Date(b.added_at) - new Date(a.added_at));
    } else if (sortBy === 'added-asc') {
      filtered.sort((a, b) => new Date(a.added_at) - new Date(b.added_at));
    } else if (sortBy === 'price-asc') {
      filtered.sort((a, b) => (a.price ?? Infinity) - (b.price ?? Infinity));
    } else if (sortBy === 'price-desc') {
      filtered.sort((a, b) => (b.price ?? -Infinity) - (a.price ?? -Infinity));
    } else if (sortBy === 'title') {
      filtered.sort((a, b) => a.title.localeCompare(b.title));
    } else if (sortBy === 'store') {
      filtered.sort((a, b) => a.store.localeCompare(b.store));
    }

    if (filtered.length === 0) {
      content.innerHTML = `<p class="empty-notice">No wishlist items match your criteria.</p>`;
      return;
    }

    // 3. Paginate
    const totalItems = filtered.length;
    const totalPages = Math.ceil(totalItems / pageSize);
    if (currentPage > totalPages) currentPage = totalPages;
    if (currentPage < 1) currentPage = 1;

    const startIndex = (currentPage - 1) * pageSize;
    const pagedItems = filtered.slice(startIndex, startIndex + pageSize);

    if (currentView === 'table') {
      renderTable(pagedItems, totalItems, totalPages);
    } else {
      renderGrid(pagedItems, totalItems, totalPages);
    }
  }

  function storeInitial(store) {
    if (!store) return '•';
    const clean = store.replace(/^www\./i, '').trim();
    return clean ? clean[0].toUpperCase() : '•';
  }

  function renderPagination(totalItems, totalPages) {
    if (totalPages <= 1) {
      return `<p class="table-count-summary">Showing all ${totalItems} item${totalItems === 1 ? '' : 's'}</p>`;
    }

    const start = (currentPage - 1) * pageSize + 1;
    const end = Math.min(currentPage * pageSize, totalItems);

    return `
      <div class="wishlist-pagination">
        <div class="pagination-info">Showing ${start}–${end} of ${totalItems} items</div>
        <div class="pagination-controls">
          <button type="button" class="btn-page" id="page-prev" ${currentPage <= 1 ? 'disabled' : ''}>← Previous</button>
          <span class="page-current">Page ${currentPage} of ${totalPages}</span>
          <button type="button" class="btn-page" id="page-next" ${currentPage >= totalPages ? 'disabled' : ''}>Next →</button>
        </div>
      </div>
    `;
  }

  function renderTable(list, totalItems, totalPages) {
    let rows = list.map(item => {
      const initial = storeInitial(item.store);
      const img = item.image_url 
        ? `<img class="table-thumb" src="${escapeHTML(item.image_url)}" alt="" loading="lazy" referrerpolicy="no-referrer">`
        : `<div class="table-thumb-placeholder" title="${escapeHTML(item.store)}">${escapeHTML(initial)}</div>`;
      
      const price = item.price_label 
        ? `<span class="col-price-value">${escapeHTML(item.price_label)}</span>` 
        : `<span class="col-price-empty">—</span>`;

      const tags = (item.tags && item.tags.length > 0)
        ? item.tags.map(t => `<span class="tag-pill">${escapeHTML(t)}</span>`).join('')
        : '';

      const status = item.unavailable
        ? `<span class="badge badge-unavailable">Out of Stock</span>`
        : `<span class="badge badge-instock">Buyable</span>`;

      const priorityBadge = (item.priority !== undefined && item.priority !== null)
        ? `<span class="badge badge-priority badge-p${item.priority}">P${item.priority}</span>`
        : '';

      const notes = item.user_notes
        ? `<div class="table-item-notes"><span class="notes-icon">📝</span> ${escapeHTML(item.user_notes)}</div>`
        : '';

      const desc = item.description 
        ? `<div class="table-item-desc">${escapeHTML(item.description)}</div>` 
        : '';

      return `
        <tr class="${item.unavailable ? 'row-unavailable' : ''}">
          <td class="col-thumb">${img}</td>
          <td class="col-title">
            <a class="item-title-link" href="${escapeHTML(item.url)}" target="_blank" rel="noopener">${escapeHTML(item.title)}</a>
            ${priorityBadge}
            ${notes}
            ${desc}
            <div class="table-mobile-meta">
              <a href="${escapeHTML(item.store_href)}">${escapeHTML(item.store)}</a> • ${price}
            </div>
          </td>
          <td class="col-store"><a href="${escapeHTML(item.store_href)}">${escapeHTML(item.store)}</a></td>
          <td class="col-price">${price}</td>
          <td class="col-tags">${tags}</td>
          <td class="col-status">${status}</td>
        </tr>
      `;
    }).join('');

    content.innerHTML = `
      <div class="table-wrap">
        <table class="wishlist-table">
          <thead>
            <tr>
              <th class="col-thumb" aria-label="Thumbnail"></th>
              <th class="col-title">Item</th>
              <th class="col-store">Store</th>
              <th class="col-price">Price</th>
              <th class="col-tags">Tags</th>
              <th class="col-status">Status</th>
            </tr>
          </thead>
          <tbody>
            ${rows}
          </tbody>
        </table>
      </div>
      ${renderPagination(totalItems, totalPages)}
    `;
    attachPaginationEvents();
  }

  function renderGrid(list, totalItems, totalPages) {
    let cards = list.map(item => {
      const initial = storeInitial(item.store);
      const img = item.image_url
        ? `<div class="grid-card-thumb"><img src="${escapeHTML(item.image_url)}" alt="" loading="lazy" referrerpolicy="no-referrer"></div>`
        : `<div class="grid-card-thumb grid-card-thumb-empty" title="${escapeHTML(item.store)}">${escapeHTML(initial)}</div>`;

      const price = item.price_label
        ? `<p class="card-price">${escapeHTML(item.price_label)}</p>`
        : '';

      const tags = (item.tags && item.tags.length > 0)
        ? `<div class="card-tags">${item.tags.map(t => `<span class="tag-pill">${escapeHTML(t)}</span>`).join('')}</div>`
        : '';

      const status = item.unavailable
        ? `<span class="badge badge-unavailable">Out of Stock</span>`
        : '';

      const priorityBadge = (item.priority !== undefined && item.priority !== null)
        ? `<span class="badge badge-priority badge-p${item.priority}">P${item.priority}</span>`
        : '';

      const notes = item.user_notes
        ? `<p class="card-notes"><span class="notes-icon">📝</span> ${escapeHTML(item.user_notes)}</p>`
        : '';

      const desc = item.description
        ? `<p class="grid-card-desc">${escapeHTML(item.description)}</p>`
        : '';

      return `
        <article class="card grid-card ${item.unavailable ? 'card-unavailable' : ''}">
          ${img}
          <div class="grid-card-body">
            <h3 class="card-title"><a href="${escapeHTML(item.url)}" target="_blank" rel="noopener">${escapeHTML(item.title)}</a></h3>
            <p class="card-store"><a href="${escapeHTML(item.store_href)}">${escapeHTML(item.store)}</a> ${status} ${priorityBadge}</p>
            ${price}
            ${notes}
            ${desc}
            ${tags}
          </div>
        </article>
      `;
    }).join('');

    content.innerHTML = `
      <div class="cards grid-cards">
        ${cards}
      </div>
      ${renderPagination(totalItems, totalPages)}
    `;
    attachPaginationEvents();
  }

  function attachPaginationEvents() {
    const prevBtn = content.querySelector('#page-prev');
    const nextBtn = content.querySelector('#page-next');
    if (prevBtn) {
      prevBtn.addEventListener('click', () => {
        if (currentPage > 1) {
          currentPage--;
          render();
          content.scrollIntoView({ behavior: 'smooth', block: 'start' });
        }
      });
    }
    if (nextBtn) {
      nextBtn.addEventListener('click', () => {
        currentPage++;
        render();
        content.scrollIntoView({ behavior: 'smooth', block: 'start' });
      });
    }
  }

  // Event handlers
  const searchInput = toolbar.querySelector('#wishlist-search');
  searchInput.addEventListener('input', e => {
    query = e.target.value.trim();
    currentPage = 1;
    render();
  });

  const sortSelect = toolbar.querySelector('#wishlist-sort');
  sortSelect.addEventListener('change', e => {
    sortBy = e.target.value;
    currentPage = 1;
    render();
  });

  const stockSelect = toolbar.querySelector('#wishlist-stock');
  stockSelect.addEventListener('change', e => {
    filterStock = e.target.value;
    currentPage = 1;
    render();
  });

  const prioritySelect = toolbar.querySelector('#wishlist-priority');
  prioritySelect.addEventListener('change', e => {
    filterPriority = e.target.value;
    currentPage = 1;
    render();
  });

  const priceSelect = toolbar.querySelector('#wishlist-price');
  priceSelect.addEventListener('change', e => {
    filterPrice = e.target.value;
    currentPage = 1;
    render();
  });

  const yearSelect = toolbar.querySelector('#wishlist-year');
  yearSelect.addEventListener('change', e => {
    filterYear = e.target.value;
    currentPage = 1;
    render();
  });

  const btnTable = toolbar.querySelector('#toggle-table');
  const btnGrid = toolbar.querySelector('#toggle-grid');

  btnTable.addEventListener('click', () => {
    currentView = 'table';
    localStorage.setItem('wishlist_view', 'table');
    btnTable.classList.add('active');
    btnGrid.classList.remove('active');
    render();
  });

  btnGrid.addEventListener('click', () => {
    currentView = 'grid';
    localStorage.setItem('wishlist_view', 'grid');
    btnGrid.classList.add('active');
    btnTable.classList.remove('active');
    render();
  });

  // Facet chip clicks
  facetBar.addEventListener('click', e => {
    const chip = e.target.closest('.facet-chip');
    if (!chip) return;
    selectedTag = chip.getAttribute('data-tag') || '';
    currentPage = 1;
    facetBar.querySelectorAll('.facet-chip').forEach(c => {
      c.classList.toggle('active', (c.getAttribute('data-tag') || '') === selectedTag);
    });
    render();
  });

  // Tag pill clicks in table or cards to quickly filter
  content.addEventListener('click', e => {
    const pill = e.target.closest('.tag-pill');
    if (!pill) return;
    const tag = pill.textContent.trim();
    if (tag) {
      selectedTag = (selectedTag === tag) ? '' : tag;
      currentPage = 1;
      facetBar.querySelectorAll('.facet-chip').forEach(c => {
        c.classList.toggle('active', (c.getAttribute('data-tag') || '') === selectedTag);
      });
      render();
    }
  });

  // Initial render
  render();
})();
