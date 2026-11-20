import './style.css';
import {
    ListMonitors,
    GetSettings,
    SaveLabel,
    SetInput,
    SetSkipSwitchConfirm,
    SetBrightness,
    SetContrast,
    AppVersion,
    GetUpdateState,
    CheckForUpdates,
    SetAutoCheckUpdates,
    StartUpdate,
    RestartApp,
    OpenDownloadPage,
} from '../wailsjs/go/main/App';
import { EventsOn } from '../wailsjs/runtime/runtime';

// Fixed input-source list (MCCS names from monitor/vcp.go). It is generic:
// the panel may not actually have every port, which the hint under the list
// makes clear.
const INPUTS = [
    { name: 'vga', label: 'VGA' },
    { name: 'dvi', label: 'DVI' },
    { name: 'dp1', label: 'DP1' },
    { name: 'dp2', label: 'DP2' },
    { name: 'hdmi1', label: 'HDMI1' },
    { name: 'hdmi2', label: 'HDMI2' },
    { name: 'usb-c', label: 'USB-C' },
];

const state = {
    monitors: [],
    error: '',
    expanded: new Set(), // monitor IDs whose "更多" section is open (not persisted)
    busy: false,
    skipSwitchConfirm: false,
    switching: false,
    autoCheck: true,
};

const $ = (sel) => document.querySelector(sel);
const content = () => $('#content');

function el(tag, className, text) {
    const n = document.createElement(tag);
    if (className) n.className = className;
    if (text !== undefined) n.textContent = text;
    return n;
}

let toastTimer = 0;
function toast(msg) {
    const t = $('#toast');
    t.textContent = msg;
    t.hidden = false;
    t.style.opacity = '1';
    clearTimeout(toastTimer);
    toastTimer = setTimeout(() => {
        t.style.opacity = '0';
        setTimeout(() => { t.hidden = true; }, 200);
    }, 3200);
}

async function refresh() {
    if (state.busy) return;
    state.busy = true;
    $('#refresh').disabled = true;
    try {
        const [monitors, settings] = await Promise.all([
            ListMonitors(),
            // Settings are a convenience; a failure must not hide the monitors.
            GetSettings().catch((err) => { console.warn('settings:', err); return null; }),
        ]);
        state.monitors = monitors || [];
        state.error = '';
        if (settings) {
            state.skipSwitchConfirm = !!settings.skipSwitchConfirm;
            state.autoCheck = !settings.disableUpdateCheck;
            for (const m of state.monitors) {
                const saved = settings.monitors?.[m.id];
                if (saved && saved.labels) m.labels = saved.labels;
            }
        }
        render();
    } catch (err) {
        state.monitors = [];
        state.error = String(err);
        render();
    } finally {
        state.busy = false;
        $('#refresh').disabled = false;
    }
}

function render() {
    const root = content();
    root.replaceChildren();
    const [first, ...others] = state.monitors;
    // The first monitor's name takes the header spot; its section then starts
    // directly with the input list. Additional monitors keep their own title.
    setTitle(first);
    if (!first) {
        root.append(emptyView());
        return;
    }
    root.append(monitorView(first, false));
    for (const m of others) root.append(monitorView(m, true));
}

function setTitle(m) {
    const h = $('#title');
    h.replaceChildren();
    if (!m) {
        h.textContent = 'monctl';
        return;
    }
    h.append(document.createTextNode(m.description || `显示器 ${m.index}`));
    h.append(document.createTextNode(' '), el('span', 'idx', `(${m.index})`));
}

function emptyView() {
    const box = el('div', 'empty');
    box.append(el('p', 'title', '未检测到可控制的显示器'));
    const ul = el('ul');
    ul.append(
        el('li', '', '显示器当前可能正在显示其他输入源'),
        el('li', '', '显示器 OSD 菜单中可能未开启 DDC/CI'),
    );
    box.append(ul);
    if (state.error) box.append(el('p', 'detail', state.error));
    const retry = el('button', '', '重新检测');
    retry.onclick = refresh;
    box.append(retry);
    return box;
}

function inputKey(m) {
    return m.input.ok ? (m.input.name || m.input.raw) : '';
}

function monitorView(m, showTitle = true) {
    const section = el('section', 'monitor');
    if (showTitle) {
        const h2 = el('h2', '', m.description || `显示器 ${m.index}`);
        h2.append(document.createTextNode(' '), el('span', 'idx', `(${m.index})`));
        section.append(h2);
    }
    section.append(el('h3', '', '输入源'));

    const labels = m.labels || {};
    const current = inputKey(m);
    const rows = el('div', 'rows');

    // Current source first, then the rest by most recent switch, with labeled
    // ports shown and the remainder folded behind "更多". Unranked ports keep
    // the fixed list order (sort is stable).
    const rank = new Map((m.recent || []).map((name, i) => [name, i]));
    const rankOf = (name) => (rank.has(name) ? rank.get(name) : rank.size);
    if (m.input.ok) {
        rows.append(row(m, current, current === m.input.name ? inputText(current) : m.input.raw, labels[current], true));
    }
    const labeled = INPUTS.filter((i) => i.name !== current && labels[i.name]).sort((a, b) => rankOf(a.name) - rankOf(b.name));
    for (const i of labeled) rows.append(row(m, i.name, i.label, labels[i.name], false));
    const rest = INPUTS.filter((i) => i.name !== current && !labels[i.name]).sort((a, b) => rankOf(a.name) - rankOf(b.name));
    if (rest.length) rows.append(moreRow(m, rest));

    if (state.expanded.has(m.id)) {
        for (const i of rest) rows.append(row(m, i.name, i.label, '', false));
    }

    section.append(rows, el('p', 'hint', '列表为通用输入源，不代表显示器实际拥有这些接口'));
    if (!m.input.ok) {
        section.append(el('p', 'warn', '无法读取当前输入源，暂不能调节亮度等设置'));
    } else {
        section.append(controls(m));
    }
    return section;
}

// controls renders brightness/contrast. A control the panel could not be
// read for is hidden rather than shown greyed out, and the whole block is
// only rendered when the current input is known.
function controls(m) {
    const box = el('div', 'controls');
    if (m.brightness?.ok) box.append(levelRow(m, 'brightness', '亮度', m.brightness.percent));
    if (m.contrast?.ok) box.append(levelRow(m, 'contrast', '对比度', m.contrast.percent));
    return box;
}

function levelRow(m, kind, label, initial) {
    const row = el('div', 'ctl-row');
    row.append(el('span', 'ctl-name', label));

    const range = document.createElement('input');
    range.type = 'range';
    range.min = '0';
    range.max = '100';
    range.step = '1';
    range.value = String(initial);
    range.className = 'slider';
    const val = el('span', 'ctl-val', `${initial}%`);
    row.append(range, val);

    let confirmed = initial;
    let timer = 0;
    let seq = 0;
    const apply = async () => {
        timer = 0;
        const want = Number(range.value);
        const my = ++seq;
        try {
            if (kind === 'brightness') await SetBrightness(m.index, want);
            else await SetContrast(m.index, want);
            if (my === seq) confirmed = want;
        } catch (err) {
            if (my !== seq) return;
            // Most likely the panel moved to another source: put the slider
            // back where the panel actually is and say why.
            console.error(`${kind}:`, err);
            seq = 0;
            range.value = String(confirmed);
            val.textContent = `${confirmed}%`;
            toast('无法与显示器通信，可能已切换到其他输入源');
        }
    };
    range.addEventListener('input', () => {
        val.textContent = `${range.value}%`;
        clearTimeout(timer);
        timer = setTimeout(apply, 180);
    });
    range.addEventListener('change', () => {
        clearTimeout(timer);
        apply();
    });
    return row;
}

function inputText(name) {
    const found = INPUTS.find((i) => i.name === name);
    return found ? found.label : name;
}

function row(m, key, inputLabel, labelValue, isCurrent) {
    const r = el('div', isCurrent ? 'row current' : 'row');
    r.append(el('span', 'dot'), el('span', 'input', inputLabel));

    const label = el('span', labelValue ? 'label' : 'label empty', labelValue || '添加名称');
    label.title = '点击修改名称';
    label.onclick = () => editLabel(label, m, key);
    r.append(label);

    if (isCurrent) {
        r.append(el('span', 'state', '当前'));
    } else {
        const btn = el('button', '', '切换');
        btn.onclick = () => switchInput(m, key, inputLabel);
        r.append(btn);
    }
    return r;
}

function moreRow(m, hidden) {
    const open = state.expanded.has(m.id);
    const names = hidden.map((i) => i.label).join('、');
    const btn = el('button', 'more');
    btn.append(
        el('span', 'caret', open ? '▾' : '▸'),
        el('span', '', `更多 (${names})`),
    );
    btn.onclick = () => {
        if (open) state.expanded.delete(m.id);
        else state.expanded.add(m.id);
        render();
    };
    return btn;
}

function editLabel(span, m, key) {
    const input = document.createElement('input');
    input.className = 'label-edit';
    input.value = m.labels?.[key] || '';
    input.placeholder = '名称';
    span.replaceWith(input);
    input.focus();
    input.select();

    let done = false;
    const finish = async (save) => {
        if (done) return;
        done = true;
        const value = input.value.trim();
        if (save && value !== (m.labels?.[key] || '')) {
            try {
                await SaveLabel(m.id, key, value);
                if (value) m.labels[key] = value;
                else delete m.labels[key];
            } catch (err) {
                toast(`保存名称失败：${err}`);
            }
        }
        render();
    };
    input.addEventListener('keydown', (e) => {
        if (e.key === 'Enter') finish(true);
        else if (e.key === 'Escape') finish(false);
    });
    input.addEventListener('blur', () => finish(true));
}

function targetText(inputLabel, label) {
    return label ? `${inputLabel}（${label}）` : inputLabel;
}

// confirmDialog resolves to {ok, checked}; checked is the optional
// "don't ask again" box.
function confirmDialog({ title, desc, okLabel = '确定', checkboxLabel = '' }) {
    return new Promise((resolve) => {
        const dlg = el('dialog', 'confirm');
        dlg.append(el('p', 'confirm-title', title));
        dlg.append(el('p', 'confirm-desc', desc));

        let dont = null;
        if (checkboxLabel) {
            dont = document.createElement('input');
            dont.type = 'checkbox';
            const row = el('label', 'dont-ask');
            row.append(dont, document.createTextNode(checkboxLabel));
            dlg.append(row);
        }

        const actions = el('div', 'confirm-actions');
        const cancel = el('button', '', '取消');
        const go = el('button', 'primary', okLabel);
        actions.append(cancel, go);
        dlg.append(actions);

        let result = { ok: false, checked: false };
        cancel.onclick = () => dlg.close();
        go.onclick = () => {
            result = { ok: true, checked: !!(dont && dont.checked) };
            dlg.close();
        };
        dlg.addEventListener('close', () => {
            dlg.remove();
            resolve(result);
        });
        document.body.append(dlg);
        dlg.showModal();
        go.focus();
    });
}

function askSwitch(inputLabel, label) {
    return confirmDialog({
        title: `切换到 ${targetText(inputLabel, label)}？`,
        desc: '切换后本机将无法控制这台显示器，需要在其他电脑或显示器上切回。',
        okLabel: '切换',
        checkboxLabel: '不再提示',
    });
}

// switchInput sends the input change. The UI is deliberately left untouched:
// the current marker stays on the source we just left, so switching back
// later shows exactly the state the user left.
async function switchInput(m, key, inputLabel) {
    if (state.switching) return;
    if (!state.skipSwitchConfirm) {
        const r = await askSwitch(inputLabel, m.labels?.[key]);
        if (!r.ok) return;
        if (r.checked) {
            try {
                await SetSkipSwitchConfirm(true);
                state.skipSwitchConfirm = true;
            } catch (err) {
                toast(`保存设置失败：${err}`);
            }
        }
    }
    const target = targetText(inputLabel, m.labels?.[key]);
    state.switching = true;
    try {
        await SetInput(m.index, key);
        toast(`已发送切换到 ${target}`);
    } catch (err) {
        // Most likely the panel already moved to another source; the design
        // says to say so and change nothing. No retry.
        console.error('set input:', err);
        toast('无法与显示器通信，可能已切换到其他输入源');
    } finally {
        state.switching = false;
    }
}

// renderUpdate drives the footer's update button. It is deliberately
// independent of the monitor state: an update is worth offering even when no
// monitor could be found. state is "working"/"done"/"failed" during and after
// an in-app install, empty while only offering the release.
function renderUpdate(view) {
    const btn = $('#update');
    const latest = view?.latest || '';

    // Start from the resting state; each branch below opts back in.
    setUpdateAction(btn, '更新', null);
    announceUpdate('');

    // Re-checking makes no sense while an install owns the updater.
    $('#check-updates').disabled = view?.state === 'working';
    if (!view) return;

    switch (view.state) {
    case 'working': {
        const step = view.step === 'install' ? '安装' : '下载';
        setUpdateAction(btn, `${step}中…`, null);
        announceUpdate(`正在${step}…`);
        return;
    }
    case 'done':
        setUpdateAction(btn, '重启以完成更新', restartAfterUpdate, 'primary', `已更新到 ${latest}`);
        announceUpdate(`已更新到 ${latest}，重启生效`);
        return;
    case 'failed': {
        const msg = failureMessage(view);
        setUpdateAction(btn, '打开下载页', openDownloadPage, 'primary', msg);
        announceUpdate(msg);
        return;
    }
    }
    if (!view.available) return;
    setUpdateAction(btn, '更新', startUpdate, 'primary', `更新到 ${latest}`);
    announceUpdate(`${latest} 可用`);
}

// announceUpdate feeds the footer's visually hidden live region so screen
// readers hear the same state changes the button colour conveys.
function announceUpdate(text) {
    const region = $('#update-status');
    if (region.textContent !== text) region.textContent = text;
}

// setUpdateAction points the footer button at one action. A null handler
// disables it; the default className is the greyed-out ghost look.
function setUpdateAction(btn, text, handler, className = 'ghost', title = '') {
    btn.textContent = text;
    btn.disabled = !handler;
    btn.onclick = handler;
    btn.className = className;
    btn.title = title;
}

function restartAfterUpdate() {
    RestartApp().catch((err) => toast(`重启失败：${err}`));
}

function openDownloadPage() {
    OpenDownloadPage().catch((err) => toast(`无法打开下载页：${err}`));
}

function failureText(reason) {
    switch (reason) {
    case 'readonly':
        return '请先把 monctl 拖入 Applications，再使用自动更新';
    case 'permission':
        return '没有写入权限，无法自动更新';
    case 'unsupported':
        return '当前运行方式不支持自动更新';
    default:
        return '自动更新失败，可从下载页手动安装';
    }
}

// failureMessage is the localized reason plus the raw backend detail, which
// matters when reason is "other" (checksum mismatch, network, ...).
function failureMessage(view) {
    const detail = view?.error ? `：${view.error}` : '';
    return failureText(view?.reason) + detail;
}

async function startUpdate() {
    const btn = $('#update');
    // Close the gap before the backend's "working" event arrives, so a second
    // click cannot start a competing install.
    setUpdateAction(btn, '更新中…', null);
    try {
        const view = await StartUpdate();
        renderUpdate(view);
        if (view.state === 'failed') toast(failureMessage(view));
    } catch (err) {
        toast(`更新失败：${err}`);
        try {
            renderUpdate(await GetUpdateState());
        } catch (stateErr) {
            console.warn('update state:', stateErr);
        }
    }
}

async function checkUpdates() {
    const btn = $('#check-updates');
    btn.disabled = true;
    try {
        const view = await CheckForUpdates();
        renderUpdate(view);
        if (view.state === 'done') return;
        if (!view.available) toast('已是最新版本');
    } catch (err) {
        const msg = String(err);
        // The messages are stable backend sentinels; see gui/update.go and
        // internal/update/update.go.
        if (msg.includes('already in progress')) toast('更新正在进行中');
        else if (msg.includes('not a release build')) toast('开发版不检查更新');
        else toast(`检查更新失败：${msg}`);
    } finally {
        btn.disabled = false;
    }
}

async function settingsDialog() {
    // Read the stored value first: state.autoCheck may still hold the default
    // if refresh() failed, and saving that would silently overwrite settings.
    let autoCheck = state.autoCheck;
    try {
        const settings = await GetSettings();
        autoCheck = !settings.disableUpdateCheck;
        state.autoCheck = autoCheck;
    } catch (err) {
        console.warn('settings:', err);
    }

    const dlg = el('dialog', 'confirm');
    const title = el('p', 'confirm-title', '设置');
    title.id = 'settings-title';
    dlg.setAttribute('aria-labelledby', title.id);
    dlg.append(title);

    const row = el('label', 'dont-ask');
    const cb = document.createElement('input');
    cb.type = 'checkbox';
    cb.checked = autoCheck;
    row.append(cb, document.createTextNode('启动时检查更新'));
    dlg.append(row);

    // Save on change so closing the dialog by any route (button, Esc) keeps
    // what the user picked.
    cb.onchange = async () => {
        const want = cb.checked;
        try {
            await SetAutoCheckUpdates(want);
            state.autoCheck = want;
        } catch (err) {
            cb.checked = !want;
            toast(`保存设置失败：${err}`);
        }
    };

    const actions = el('div', 'confirm-actions');
    const done = el('button', 'primary', '关闭');
    actions.append(done);
    dlg.append(actions);

    done.onclick = () => dlg.close();
    dlg.addEventListener('close', () => dlg.remove());
    document.body.append(dlg);
    dlg.showModal();
    cb.focus();
}

async function init() {
    $('#check-updates').onclick = checkUpdates;
    $('#settings').onclick = settingsDialog;
    // The automatic startup check runs in the backend and reports here.
    EventsOn('update:state', (view) => renderUpdate(view));

    try {
        const v = await AppVersion();
        $('#version').textContent = v ? `monctl ${v}` : '';
    } catch (err) {
        console.warn('version:', err);
    }
    try {
        renderUpdate(await GetUpdateState());
    } catch (err) {
        console.warn('update state:', err);
    }
    refresh();
}

$('#refresh').onclick = refresh;
init();
