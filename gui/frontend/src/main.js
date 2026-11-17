import './style.css';
import { ListMonitors, GetSettings, SaveLabel, SetInput, SetSkipSwitchConfirm } from '../wailsjs/go/main/App';

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
    if (state.monitors.length === 0) {
        root.append(emptyView());
        return;
    }
    for (const m of state.monitors) root.append(monitorView(m));
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

function monitorView(m) {
    const section = el('section', 'monitor');
    const h2 = el('h2', '', m.description || `显示器 ${m.index}`);
    h2.append(document.createTextNode(' '), el('span', 'idx', `(${m.index})`));
    section.append(h2, el('h3', '', '输入源'));

    const labels = m.labels || {};
    const current = inputKey(m);
    const rows = el('div', 'rows');

    // Current source first, then labeled ports, then the rest behind "更多".
    if (m.input.ok) {
        rows.append(row(m, current, current === m.input.name ? inputText(current) : m.input.raw, labels[current], true));
    }
    const labeled = INPUTS.filter((i) => i.name !== current && labels[i.name]);
    for (const i of labeled) rows.append(row(m, i.name, i.label, labels[i.name], false));
    const rest = INPUTS.filter((i) => i.name !== current && !labels[i.name]);
    if (rest.length) rows.append(moreRow(m, rest));

    if (state.expanded.has(m.id)) {
        for (const i of rest) rows.append(row(m, i.name, i.label, '', false));
    }

    section.append(rows, el('p', 'hint', '列表为通用输入源，不代表显示器实际拥有这些接口'));
    if (!m.input.ok) {
        section.append(el('p', 'warn', '无法读取当前输入源，暂不能调节亮度等设置'));
    }
    return section;
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

// askSwitch shows the confirmation dialog and resolves to {ok, skip}.
function askSwitch(inputLabel, label) {
    return new Promise((resolve) => {
        const dlg = el('dialog', 'confirm');
        dlg.append(el('p', 'confirm-title', `切换到 ${targetText(inputLabel, label)}？`));
        dlg.append(el('p', 'confirm-desc', '切换后本机将无法控制这台显示器，需要在其他电脑或显示器上切回。'));

        const dont = document.createElement('input');
        dont.type = 'checkbox';
        const dontRow = el('label', 'dont-ask');
        dontRow.append(dont, document.createTextNode('不再提示'));
        dlg.append(dontRow);

        const actions = el('div', 'confirm-actions');
        const cancel = el('button', '', '取消');
        const go = el('button', 'primary', '切换');
        actions.append(cancel, go);
        dlg.append(actions);

        let result = { ok: false, skip: false };
        cancel.onclick = () => dlg.close();
        go.onclick = () => {
            result = { ok: true, skip: dont.checked };
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

// switchInput sends the input change. The UI is deliberately left untouched:
// the current marker stays on the source we just left, so switching back
// later shows exactly the state the user left.
async function switchInput(m, key, inputLabel) {
    if (state.switching) return;
    if (!state.skipSwitchConfirm) {
        const r = await askSwitch(inputLabel, m.labels?.[key]);
        if (!r.ok) return;
        if (r.skip) {
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

$('#refresh').onclick = refresh;
refresh();
