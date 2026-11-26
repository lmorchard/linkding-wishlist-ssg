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
  let sortBy = 'default'; // 'default', 'price-asc', 'price-desc', 'title', 'store'
  let filterStock = 'all'; // 'all', 'instock', 'unavailable'

  // Render controls toolbar
  const toolbar = document.createElement('div');
  toolbar.className = 'wishlist-toolbar';
  toolbar.innerHTML = `
    <div class="toolbar-search">
      <input type="search" id="wishlist-search" placeholder="Search items, stores, tags..." aria-label="Search wishlist">
    </div>
    <div class="toolbar-filters">
      <select id="wishlist-sort" aria-label="Sort by">
        <option value="default">Sort: Default</option>
        <option value="price-asc">Price: Low to High</option>
        <option value="price-desc">Price: High to Low</option>
        <option value="title">Title: A to Z</option>
        <option value="store">Store</option>
      </select>
      <select id="wishlist-stock" aria-label="Filter availability">
        <option value="all">All Items</option>
        <option value="instock">In Stock Only</option>
        <option value="unavailable">Unavailable Only</option>
      </select>
      <div class="toolbar-view-toggle">
        <button type="button" id="toggle-table" class="${currentView === 'table' ? 'active' : ''}" title="Table View">☷ Table</button>
        <button type="button" id="toggle-grid" class="${currentView === 'grid' ? 'active' : ''}" title="Grid View">⊞ Grid</button>
      </div>
    </div>
  `;

  // Content container
  const content = document.createElement('div');
  content.className = 'wishlist-interactive-content';

  // Replace server-rendered view with interactive container
  container.innerHTML = '';
  container.appendChild(toolbar);
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
      if (query) {
        const q = query.toLowerCase();
        const inTitle = item.title.toLowerCase().includes(q);
        const inStore = item.store.toLowerCase().includes(q);
        const inDesc = item.description ? item.description.toLowerCase().includes(q) : false;
        const inTags = item.tags ? item.tags.some(t => t.toLowerCase().includes(q)) : false;
        if (!inTitle && !inStore && !inDesc && !inTags) return false;
      }
      return true;
    });

    // 2. Sort
    if (sortBy === 'price-asc') {
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

    if (currentView === 'table') {
      renderTable(filtered);
    } else {
      renderGrid(filtered);
    }
  }

  function renderTable(list) {
    let rows = list.map(item => {
      const img = item.image_url 
        ? `<img class="table-thumb" src="${escapeHTML(item.image_url)}" alt="" loading="lazy" referrerpolicy="no-referrer">`
        : `<div class="table-thumb-placeholder"></div>`;
      
      const price = item.price_label 
        ? `<span class="col-price-value">${escapeHTML(item.price_label)}</span>` 
        : `<span class="col-price-empty">—</span>`;

      const tags = (item.tags && item.tags.length > 0)
        ? item.tags.map(t => `<span class="tag-pill">${escapeHTML(t)}</span>`).join('')
        : '';

      const status = item.unavailable
        ? `<span class="badge badge-unavailable">Out of Stock</span>`
        : `<span class="badge badge-instock">Buyable</span>`;

      const desc = item.description 
        ? `<div class="table-item-desc">${escapeHTML(item.description)}</div>` 
        : '';

      return `
        <tr class="${item.unavailable ? 'row-unavailable' : ''}">
          <td class="col-thumb">${img}</td>
          <td class="col-title">
            <a class="item-title-link" href="${escapeHTML(item.url)}" target="_blank" rel="noopener">${escapeHTML(item.title)}</a>
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
      <p class="table-count-summary">Showing ${list.length} item${list.length === 1 ? '' : 's'}</p>
    `;
  }

  function renderGrid(list) {
    let cards = list.map(item => {
      const img = item.image_url
        ? `<div class="grid-card-thumb"><img src="${escapeHTML(item.image_url)}" alt="" loading="lazy" referrerpolicy="no-referrer"></div>`
        : `<div class="grid-card-thumb grid-card-thumb-empty"></div>`;

      const price = item.price_label
        ? `<p class="card-price">${escapeHTML(item.price_label)}</p>`
        : '';

      const tags = (item.tags && item.tags.length > 0)
        ? `<div class="card-tags">${item.tags.map(t => `<span class="tag-pill">${escapeHTML(t)}</span>`).join('')}</div>`
        : '';

      const status = item.unavailable
        ? `<span class="badge badge-unavailable">Out of Stock</span>`
        : '';

      const desc = item.description
        ? `<p class="grid-card-desc">${escapeHTML(item.description)}</p>`
        : '';

      return `
        <article class="card grid-card ${item.unavailable ? 'card-unavailable' : ''}">
          ${img}
          <div class="grid-card-body">
            <h3 class="card-title"><a href="${escapeHTML(item.url)}" target="_blank" rel="noopener">${escapeHTML(item.title)}</a></h3>
            <p class="card-store"><a href="${escapeHTML(item.store_href)}">${escapeHTML(item.store)}</a> ${status}</p>
            ${price}
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
      <p class="table-count-summary">Showing ${list.length} item${list.length === 1 ? '' : 's'}</p>
    `;
  }

  // Event handlers
  const searchInput = toolbar.querySelector('#wishlist-search');
  searchInput.addEventListener('input', e => {
    query = e.target.value.trim();
    render();
  });

  const sortSelect = toolbar.querySelector('#wishlist-sort');
  sortSelect.addEventListener('change', e => {
    sortBy = e.target.value;
    render();
  });

  const stockSelect = toolbar.querySelector('#wishlist-stock');
  stockSelect.addEventListener('change', e => {
    filterStock = e.target.value;
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

  // Initial render
  render();
})();
