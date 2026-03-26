// --- Team Builder ---

var _teamBuilderLoaded = false;

function refreshTeamBuilder() {
  var container = document.getElementById('team-builder-content');
  if (!container) return;
  if (!_teamBuilderLoaded) {
    renderTeamBuilderView();
    _teamBuilderLoaded = true;
  }
  loadTeamList();
}

function renderTeamBuilderView() {
  var container = document.getElementById('team-builder-content');
  container.innerHTML = [
    '<div class="section">',
    '  <div class="tb-header">',
    '    <div class="tb-header-left">',
    '      <span class="tb-title">Team Builder</span>',
    '      <span class="tb-subtitle">Create and manage AI agent teams. Generate a team from a description or use a built-in template.</span>',
    '    </div>',
    '    <div class="tb-actions">',
    '      <button class="btn" onclick="loadTeamList()">Refresh</button>',
    '      <button class="btn btn-run" onclick="openTeamGenerateModal()">+ Generate Team</button>',
    '    </div>',
    '  </div>',
    '  <div id="team-list"></div>',
    '</div>',

    // Generate modal
    '<div id="team-generate-modal" class="tb-modal-overlay" onclick="if(event.target===this)closeTeamGenerateModal()">',
    '  <div class="tb-modal" style="min-width:460px;max-width:520px">',
    '    <button class="modal-close" onclick="closeTeamGenerateModal()">&times;</button>',
    '    <div class="tb-modal-title">Generate New Team</div>',
    '    <form id="team-generate-form" onsubmit="submitTeamGenerate(event)">',
    '      <div class="form-row">',
    '        <label>Description</label>',
    '        <textarea id="tg-description" rows="4" placeholder="Describe the team you want, e.g.: A data engineering team for ETL pipelines, data quality, and analytics" required></textarea>',
    '      </div>',
    '      <div class="form-row-inline">',
    '        <div class="form-row">',
    '          <label>Team Size</label>',
    '          <input type="number" id="tg-size" min="2" max="10" placeholder="Auto">',
    '        </div>',
    '        <div class="form-row">',
    '          <label>Base Template</label>',
    '          <select id="tg-template">',
    '            <option value="">None</option>',
    '            <option value="software-dev">Software Dev</option>',
    '            <option value="content-creation">Content Creation</option>',
    '            <option value="customer-support">Customer Support</option>',
    '          </select>',
    '        </div>',
    '      </div>',
    '      <div id="tg-status" class="tb-progress hidden"></div>',
    '      <div class="form-actions">',
    '        <button type="button" class="btn" onclick="closeTeamGenerateModal()">Cancel</button>',
    '        <button type="submit" class="btn btn-run" id="tg-submit">Generate</button>',
    '      </div>',
    '    </form>',
    '  </div>',
    '</div>',

    // Detail modal
    '<div id="team-detail-modal" class="tb-modal-overlay" onclick="if(event.target===this)closeTeamDetailModal()">',
    '  <div class="tb-modal" style="min-width:460px;max-width:700px">',
    '    <button class="modal-close" onclick="closeTeamDetailModal()">&times;</button>',
    '    <div class="tb-modal-title" id="td-title">Team Details</div>',
    '    <div id="td-body"></div>',
    '    <div id="td-actions" class="tb-detail-actions"></div>',
    '  </div>',
    '</div>'
  ].join('\n');
}

function _tbTeamInitial(name) {
  if (!name) return '?';
  return name.charAt(0).toUpperCase();
}

async function loadTeamList() {
  var list = document.getElementById('team-list');
  if (!list) return;
  list.innerHTML = '<div style="color:var(--muted);padding:20px;text-align:center;font-size:12px">Loading teams...</div>';

  try {
    var teams = await fetchJSON('/api/teams');
    if (!Array.isArray(teams) || teams.length === 0) {
      list.innerHTML = [
        '<div class="tb-empty">',
        '  <div class="tb-empty-icon">&#x1f465;</div>',
        '  <div class="tb-empty-title">No teams yet</div>',
        '  <div class="tb-empty-desc">Teams let you group agents with complementary skills. Generate your first team from a natural language description.</div>',
        '  <button class="btn btn-run" onclick="openTeamGenerateModal()">+ Generate Team</button>',
        '</div>'
      ].join('\n');
      return;
    }

    var html = '<div class="tb-grid">';
    teams.forEach(function(t) {
      var badge = t.builtin ? '<span class="tb-badge-builtin">builtin</span>' : '';
      html += '<div class="tb-card" onclick="openTeamDetail(\'' + esc(t.name) + '\')">';
      html += '  <div class="tb-card-top">';
      html += '    <div class="tb-card-name">';
      html += '      <span class="tb-card-icon">' + esc(_tbTeamInitial(t.name)) + '</span>';
      html += '      <span>' + esc(t.name) + '</span>';
      html += '      ' + badge;
      html += '    </div>';
      html += '    <span class="tb-card-count">' + t.agentCount + ' agents</span>';
      html += '  </div>';
      html += '  <div class="tb-card-desc">' + esc(t.description) + '</div>';
      html += '</div>';
    });
    html += '</div>';
    list.innerHTML = html;
  } catch (e) {
    list.innerHTML = '<div style="color:var(--red);padding:20px;font-size:12px">Error loading teams: ' + esc(e.message) + '</div>';
  }
}

function openTeamGenerateModal() {
  document.getElementById('team-generate-form').reset();
  var status = document.getElementById('tg-status');
  status.className = 'tb-progress hidden';
  status.innerHTML = '';
  document.getElementById('tg-submit').disabled = false;
  document.getElementById('team-generate-modal').classList.add('open');
}

function closeTeamGenerateModal() {
  document.getElementById('team-generate-modal').classList.remove('open');
}

async function submitTeamGenerate(e) {
  e.preventDefault();
  var desc = document.getElementById('tg-description').value.trim();
  if (!desc) return;

  var size = parseInt(document.getElementById('tg-size').value) || 0;
  var template = document.getElementById('tg-template').value;
  var btn = document.getElementById('tg-submit');
  var status = document.getElementById('tg-status');

  btn.disabled = true;
  btn.textContent = 'Generating...';
  status.className = 'tb-progress active';
  status.innerHTML = '<span class="tb-spinner"></span><span>Generating team\u2026 this may take a minute.</span>';

  try {
    var payload = { description: desc };
    if (size > 0) payload.size = size;
    if (template) payload.template = template;

    var resp = await fetch(API + '/api/teams/generate', {
      method: 'POST',
      headers: authHeaders({'Content-Type': 'application/json'}),
      body: JSON.stringify(payload)
    });
    if (!resp.ok) {
      var err = await resp.json();
      throw new Error(err.error || resp.statusText);
    }
    var team = await resp.json();

    status.innerHTML = '<span class="tb-spinner"></span><span>Team generated. Saving\u2026</span>';

    // Save the team.
    var resp2 = await fetch(API + '/api/teams', {
      method: 'POST',
      headers: authHeaders({'Content-Type': 'application/json'}),
      body: JSON.stringify(team)
    });
    if (!resp2.ok) {
      var err2 = await resp2.json();
      throw new Error(err2.error || resp2.statusText);
    }

    closeTeamGenerateModal();
    toast('Team "' + team.name + '" created with ' + team.agents.length + ' agents');
    loadTeamList();
  } catch (err) {
    status.className = 'tb-progress active error';
    status.innerHTML = '<span class="tb-spinner"></span><span style="color:var(--red)">Error: ' + esc(err.message) + '</span>';
  } finally {
    btn.disabled = false;
    btn.textContent = 'Generate';
  }
}

function _tbModelTier(model) {
  if (!model) return { cls: 'tb-model-balanced', label: model || 'unknown' };
  var m = model.toLowerCase();
  if (m.indexOf('haiku') !== -1 || m.indexOf('flash') !== -1 || m.indexOf('mini') !== -1) {
    return { cls: 'tb-model-fast', label: model };
  }
  if (m.indexOf('opus') !== -1 || m.indexOf('pro') !== -1 || m.indexOf('deep') !== -1) {
    return { cls: 'tb-model-deep', label: model };
  }
  return { cls: 'tb-model-balanced', label: model };
}

async function openTeamDetail(name) {
  var title = document.getElementById('td-title');
  var body = document.getElementById('td-body');
  var actions = document.getElementById('td-actions');
  title.textContent = 'Loading...';
  body.innerHTML = '';
  actions.innerHTML = '';
  document.getElementById('team-detail-modal').classList.add('open');

  try {
    var team = await fetchJSON('/api/teams/' + encodeURIComponent(name));
    title.textContent = team.name + (team.builtin ? ' (builtin)' : '');

    var html = '<div class="tb-detail-desc">' + esc(team.description) + '</div>';
    html += '<div class="tb-agent-grid">';
    (team.agents || []).forEach(function(a) {
      var tier = _tbModelTier(a.model);
      html += '<div class="tb-agent-card">';
      html += '  <div class="tb-agent-top">';
      html += '    <span class="tb-agent-name">' + esc(a.displayName || a.key) + '</span>';
      html += '    <span class="tb-model-badge ' + tier.cls + '">' + esc(tier.label) + '</span>';
      html += '  </div>';
      html += '  <div class="tb-agent-desc">' + esc(a.description) + '</div>';
      if (a.keywords && a.keywords.length > 0) {
        var kw = a.keywords.slice(0, 8);
        html += '<div class="tb-keywords">';
        kw.forEach(function(k) {
          html += '<span class="tb-keyword">' + esc(k) + '</span>';
        });
        if (a.keywords.length > 8) html += '<span class="tb-keyword-more">+' + (a.keywords.length - 8) + '</span>';
        html += '</div>';
      }
      html += '</div>';
    });
    html += '</div>';
    body.innerHTML = html;

    // Actions.
    var actHtml = '';
    if (!team.builtin) {
      actHtml += '<button class="btn btn-del" onclick="deleteTeam(\'' + esc(name) + '\')">Delete</button>';
    }
    actHtml += '<button class="btn" onclick="applyTeam(\'' + esc(name) + '\',true)">Force Apply</button>';
    actHtml += '<button class="btn btn-run" onclick="applyTeam(\'' + esc(name) + '\',false)">Apply to Config</button>';
    actions.innerHTML = actHtml;
  } catch (err) {
    body.innerHTML = '<div style="color:var(--red);font-size:12px">Error: ' + esc(err.message) + '</div>';
  }
}

function closeTeamDetailModal() {
  document.getElementById('team-detail-modal').classList.remove('open');
}

async function applyTeam(name, force) {
  var label = force ? 'Force applying' : 'Applying';
  if (!confirm(label + ' team "' + name + '" will add agents to your config. Continue?')) return;

  try {
    var resp = await fetch(API + '/api/teams/' + encodeURIComponent(name) + '/apply', {
      method: 'POST',
      headers: authHeaders({'Content-Type': 'application/json'}),
      body: JSON.stringify({ force: force })
    });
    if (!resp.ok) {
      var err = await resp.json();
      throw new Error(err.error || resp.statusText);
    }
    toast('Team "' + name + '" applied. Config reloaded.');
    closeTeamDetailModal();
  } catch (err) {
    toast('Error: ' + err.message);
  }
}

async function deleteTeam(name) {
  if (!confirm('Delete team "' + name + '"? This cannot be undone.')) return;
  try {
    var resp = await fetch(API + '/api/teams/' + encodeURIComponent(name), {
      method: 'DELETE',
      headers: authHeaders()
    });
    if (!resp.ok) {
      var err = await resp.json();
      throw new Error(err.error || resp.statusText);
    }
    toast('Team "' + name + '" deleted.');
    closeTeamDetailModal();
    loadTeamList();
  } catch (err) {
    toast('Error: ' + err.message);
  }
}
