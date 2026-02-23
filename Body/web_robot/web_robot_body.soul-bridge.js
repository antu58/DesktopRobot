(function (global, document) {
    const TERMINAL_ID_STORAGE_KEY = 'web_robot_body_terminal_id';
    const USER_ID_STORAGE_KEY = 'web_robot_body_user_id';
    const SOUL_HINT_STORAGE_KEY = 'web_robot_body_soul_hint';

    const INTENT_CONFIGS = [
        {
            id: 'intent_face_glee',
            label: '笑一个',
            triggers: ['笑一个'],
            emotion: 'Glee',
            minMs: 4000,
            maxMs: 5000,
            note: '触发 Glee，4-5 秒后恢复默认'
        },
        {
            id: 'intent_face_sad',
            label: '伤心/哭一个',
            triggers: ['伤心', '哭一个'],
            emotion: 'Sad',
            minMs: 4000,
            maxMs: 5000,
            note: '触发 Sad，4-5 秒后恢复默认'
        },
        {
            id: 'intent_face_focused',
            label: '犀利的眼神',
            triggers: ['犀利的眼神'],
            emotion: 'Focused',
            minMs: 3000,
            maxMs: 4000,
            note: '触发 Focused，3-4 秒后恢复默认'
        },
        {
            id: 'intent_face_squint',
            label: '坤坤梗',
            triggers: ['鸡你太美', '只因你太美', '坤坤', '你懂的'],
            emotion: 'Squint',
            minMs: 4000,
            maxMs: 5000,
            note: '触发 Squint，4-5 秒后恢复默认'
        }
    ];

    function nowISO() {
        return new Date().toISOString();
    }

    function nowTimeText() {
        return new Date().toLocaleTimeString();
    }

    function clampInt(value, min, max) {
        const n = Number(value);
        if (!Number.isFinite(n)) return min;
        return Math.max(min, Math.min(max, Math.round(n)));
    }

    function randomDurationMs(minMs, maxMs) {
        const min = clampInt(minMs, 100, 60 * 1000);
        const max = clampInt(maxMs, min, 60 * 1000);
        return Math.floor(Math.random() * (max - min + 1)) + min;
    }

    function normalizeTriggerText(text) {
        return String(text || '')
            .toLowerCase()
            .replace(/[\s`~!@#$%^&*()_+\-=\[\]{};':"\\|,.<>/?，。！？、；：“”‘’（）【】《》…￥·]/g, '');
    }

    function createDiv(className, text) {
        const node = document.createElement('div');
        if (className) node.className = className;
        if (typeof text === 'string') node.textContent = text;
        return node;
    }

    function safeGetStorage(key, fallback) {
        try {
            const value = global.localStorage.getItem(key);
            return value == null ? fallback : String(value);
        } catch (_e) {
            return fallback;
        }
    }

    function safeSetStorage(key, value) {
        try {
            global.localStorage.setItem(key, String(value || ''));
        } catch (_e) {
            // ignore
        }
    }

    function trimTrailingSlash(v) {
        return String(v || '').replace(/\/+$/, '');
    }

    function generateSessionId() {
        return 's-' + Date.now();
    }

    function initSoulBridge() {
        const view = {
            terminalIdInput: document.getElementById('terminalIdInput'),
            userIdInput: document.getElementById('userIdInput'),
            soulHintInput: document.getElementById('soulHintInput'),
            soulSelect: document.getElementById('soulSelect'),
            refreshSoulsBtn: document.getElementById('refreshSoulsBtn'),
            bindSoulBtn: document.getElementById('bindSoulBtn'),
            selectedSoulInfo: document.getElementById('selectedSoulInfo'),
            newSoulNameInput: document.getElementById('newSoulNameInput'),
            newSoulMbtiSelect: document.getElementById('newSoulMbtiSelect'),
            createSoulBtn: document.getElementById('createSoulBtn'),
            connectStateBtn: document.getElementById('connectStateBtn'),
            newSessionBtn: document.getElementById('newSessionBtn'),
            connectionStatus: document.getElementById('connectionStatus'),
            activeSessionInfo: document.getElementById('activeSessionInfo'),
            intentConfigList: document.getElementById('intentConfigList'),
            conversationList: document.getElementById('conversationList'),
            logLines: document.getElementById('logLines'),
            chatInput: document.getElementById('chatInput'),
            sendBtn: document.getElementById('sendBtn')
        };

        if (!view.terminalIdInput || !view.conversationList || !view.logLines || !view.chatInput || !view.sendBtn) {
            return;
        }

        let activeSessionId = generateSessionId();
        let faceRestoreTimer = null;
        let intentOverrideUntil = 0;
        let currentBoundSoulID = '';
        let loadedSouls = [];
        const logs = [];
        const conversations = {};

        function setConnectionStatus(text, isError) {
            view.connectionStatus.textContent = text;
            view.connectionStatus.className = isError ? 'text-xs text-red-600' : 'text-xs text-emerald-600';
        }

        function appendLog(line) {
            logs.push(nowISO() + ' ' + line);
            if (logs.length > 240) {
                logs.splice(0, logs.length - 240);
            }
            renderLogs();
        }

        function renderLogs() {
            view.logLines.innerHTML = '';
            if (logs.length === 0) {
                view.logLines.appendChild(createDiv('', '[LOG] 暂无日志'));
                return;
            }
            logs.forEach(function (line) {
                const cls = line.indexOf('[local_intent]') >= 0 ? 'text-cyan-300' : '';
                view.logLines.appendChild(createDiv(cls, line));
            });
            const scroller = view.logLines.parentElement;
            if (scroller) scroller.scrollTop = scroller.scrollHeight;
        }

        function getTurns(sessionId) {
            if (!conversations[sessionId]) {
                conversations[sessionId] = [];
            }
            return conversations[sessionId];
        }

        function getUserId() {
            return (view.userIdInput && view.userIdInput.value ? view.userIdInput.value : '').trim();
        }

        function getTerminalId() {
            return (view.terminalIdInput && view.terminalIdInput.value ? view.terminalIdInput.value : '').trim();
        }

        function getSelectedSoulId() {
            if (view.soulSelect && view.soulSelect.value) {
                return view.soulSelect.value.trim();
            }
            return '';
        }

        function findSoulById(soulID) {
            const id = String(soulID || '').trim();
            if (!id) return null;
            for (let i = 0; i < loadedSouls.length; i++) {
                if (String(loadedSouls[i].soul_id || '').trim() === id) {
                    return loadedSouls[i];
                }
            }
            return null;
        }

        function setSelectedSoulInfo(text, isError) {
            if (!view.selectedSoulInfo) return;
            view.selectedSoulInfo.textContent = text;
            view.selectedSoulInfo.className = isError
                ? 'text-xs text-red-600 self-center'
                : 'text-xs text-gray-500 self-center';
        }

        function updateSelectedSoulInfo() {
            const selectedSoulID = getSelectedSoulId();
            if (!selectedSoulID) {
                setSelectedSoulInfo('当前灵魂：未选择', true);
                updateSessionInfo();
                return;
            }
            const soul = findSoulById(selectedSoulID);
            const title = soul ? (soul.name + ' (' + soul.mbti_type + ')') : selectedSoulID;
            const status = currentBoundSoulID === selectedSoulID ? '已绑定' : '未绑定';
            setSelectedSoulInfo('当前灵魂：' + title + ' [' + status + ']', false);
            updateSessionInfo();
        }

        function renderConversation() {
            const turns = getTurns(activeSessionId);
            const container = view.conversationList;
            container.innerHTML = '';

            if (turns.length === 0) {
                const row = createDiv('flex');
                row.appendChild(createDiv('bg-white p-4 rounded-2xl rounded-tl-none shadow-sm max-w-xl text-gray-500', '等待会话内容...'));
                container.appendChild(row);
                return;
            }

            turns.forEach(function (turn) {
                const isUser = turn.role === 'user';
                const row = createDiv(isUser ? 'flex justify-end' : 'flex');
                const bubble = createDiv(
                    isUser
                        ? 'bg-blue-500 text-white p-4 rounded-2xl rounded-tr-none shadow-sm max-w-xl'
                        : 'bg-white p-4 rounded-2xl rounded-tl-none shadow-sm max-w-xl text-gray-700'
                );

                if (turn.timeText) {
                    bubble.appendChild(createDiv(isUser ? 'text-xs text-blue-100 mb-1' : 'text-xs text-gray-400 mb-1', turn.timeText));
                }
                bubble.appendChild(createDiv('whitespace-pre-wrap break-words', turn.content || ''));
                if (Array.isArray(turn.executedSkills) && turn.executedSkills.length > 0) {
                    bubble.appendChild(createDiv(isUser ? 'text-xs text-blue-100 mt-2' : 'text-xs text-gray-500 mt-2', 'skills=' + turn.executedSkills.join('+')));
                }

                row.appendChild(bubble);
                container.appendChild(row);
            });

            const scroller = container.parentElement;
            if (scroller) scroller.scrollTop = scroller.scrollHeight;
        }

        function updateSessionInfo() {
            const terminalId = getTerminalId() || '-';
            const soulID = currentBoundSoulID || getSelectedSoulId() || '-';
            view.activeSessionInfo.textContent = '当前会话：' + activeSessionId + ' | terminal_id：' + terminalId + ' | soul_id：' + soulID;
        }

        function getApiBase() {
            return '';
        }

        function buildURL(path) {
            const base = getApiBase();
            return base ? (base + path) : path;
        }

        async function fetchJSON(path, options) {
            const response = await fetch(buildURL(path), Object.assign({ method: 'GET' }, options || {}));
            const text = await response.text();
            let data = {};
            try {
                data = text ? JSON.parse(text) : {};
            } catch (_e) {
                data = { raw: text };
            }

            if (!response.ok) {
                const msg = data.error || data.message || ('HTTP ' + response.status);
                throw new Error(msg);
            }
            return data;
        }

        function renderIntentConfigs() {
            view.intentConfigList.innerHTML = '';
            INTENT_CONFIGS.forEach(function (cfg) {
                const card = createDiv('rounded-lg border border-gray-200 bg-white p-3');
                card.appendChild(createDiv('text-sm font-medium text-gray-800', cfg.label + ' -> ' + cfg.emotion));
                card.appendChild(createDiv('text-xs text-gray-500 mt-1', '触发词: ' + cfg.triggers.join(' / ')));
                card.appendChild(createDiv('text-xs text-blue-600 mt-1', cfg.note));
                view.intentConfigList.appendChild(card);
            });
        }

        function renderSoulOptions(items, preferredSoulID) {
            loadedSouls = Array.isArray(items) ? items.slice() : [];
            if (!view.soulSelect) {
                updateSessionInfo();
                return;
            }
            view.soulSelect.innerHTML = '';

            if (loadedSouls.length === 0) {
                currentBoundSoulID = '';
                if (view.soulHintInput) {
                    view.soulHintInput.value = '';
                }
                persistConfig();
                const emptyOption = document.createElement('option');
                emptyOption.value = '';
                emptyOption.textContent = '当前用户下暂无灵魂，请先创建';
                view.soulSelect.appendChild(emptyOption);
                updateSelectedSoulInfo();
                return;
            }

            loadedSouls.forEach(function (item) {
                const option = document.createElement('option');
                const soulID = String(item.soul_id || '').trim();
                option.value = soulID;
                option.textContent = String(item.name || soulID) + ' (' + String(item.mbti_type || '-') + ')';
                view.soulSelect.appendChild(option);
            });

            const preferred = String(preferredSoulID || '').trim();
            const fallback = currentBoundSoulID || (view.soulHintInput && view.soulHintInput.value ? view.soulHintInput.value.trim() : '');
            const targetSoulID = preferred || fallback;
            if (targetSoulID) {
                const found = loadedSouls.some(function (item) {
                    return String(item.soul_id || '').trim() === targetSoulID;
                });
                if (found) {
                    view.soulSelect.value = targetSoulID;
                }
            }
            if (currentBoundSoulID) {
                const boundExists = loadedSouls.some(function (item) {
                    return String(item.soul_id || '').trim() === currentBoundSoulID;
                });
                if (!boundExists) {
                    currentBoundSoulID = '';
                    if (view.soulHintInput) {
                        view.soulHintInput.value = '';
                    }
                    persistConfig();
                }
            }
            updateSelectedSoulInfo();
        }

        async function refreshSouls(preferredSoulID) {
            const userID = getUserId();
            if (!userID) {
                setConnectionStatus('请先输入 user_id', true);
                setSelectedSoulInfo('当前灵魂：缺少 user_id', true);
                appendLog('[souls] 读取失败：user_id 不能为空');
                return;
            }
            try {
                const out = await fetchJSON('/v1/souls?user_id=' + encodeURIComponent(userID));
                const items = Array.isArray(out.items) ? out.items : [];
                renderSoulOptions(items, preferredSoulID);
                appendLog('[souls] user=' + userID + ' -> count=' + items.length);
                if (items.length === 0) {
                    setConnectionStatus('已连接，当前用户暂无灵魂，请先创建', false);
                }
            } catch (err) {
                const message = err && err.message ? err.message : String(err);
                setConnectionStatus('读取灵魂失败：' + message, true);
                setSelectedSoulInfo('当前灵魂：读取失败', true);
                appendLog('[souls] 读取失败: ' + message);
            }
        }

        async function bindSelectedSoul() {
            const userID = getUserId();
            const terminalID = getTerminalId();
            const soulID = getSelectedSoulId();

            if (!userID) {
                setConnectionStatus('绑定失败：user_id 不能为空', true);
                appendLog('[souls/select] user_id 为空，已拦截请求');
                return;
            }
            if (!terminalID) {
                setConnectionStatus('绑定失败：terminal_id 不能为空', true);
                appendLog('[souls/select] terminal_id 为空，已拦截请求');
                return;
            }
            if (!soulID) {
                setConnectionStatus('绑定失败：请先选择灵魂', true);
                appendLog('[souls/select] soul_id 为空，已拦截请求');
                return;
            }

            try {
                const out = await fetchJSON('/v1/souls/select', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({
                        user_id: userID,
                        terminal_id: terminalID,
                        soul_id: soulID
                    })
                });
                currentBoundSoulID = String(out.soul_id || soulID).trim();
                if (view.soulHintInput) {
                    view.soulHintInput.value = currentBoundSoulID;
                }
                persistConfig();
                updateSelectedSoulInfo();
                setConnectionStatus('已绑定灵魂：' + currentBoundSoulID, false);
                appendLog('[souls/select] user=' + userID + ' terminal=' + terminalID + ' soul=' + currentBoundSoulID);
            } catch (err) {
                const message = err && err.message ? err.message : String(err);
                setConnectionStatus('绑定灵魂失败：' + message, true);
                appendLog('[souls/select] 失败: ' + message);
            }
        }

        async function createSoul() {
            const userID = getUserId();
            const name = view.newSoulNameInput && view.newSoulNameInput.value ? view.newSoulNameInput.value.trim() : '';
            const mbti = view.newSoulMbtiSelect && view.newSoulMbtiSelect.value ? view.newSoulMbtiSelect.value.trim().toUpperCase() : 'INTJ';

            if (!userID) {
                setConnectionStatus('创建失败：user_id 不能为空', true);
                appendLog('[souls/create] user_id 为空，已拦截请求');
                return;
            }
            if (!name) {
                setConnectionStatus('创建失败：灵魂名称不能为空', true);
                appendLog('[souls/create] name 为空，已拦截请求');
                return;
            }

            try {
                const out = await fetchJSON('/v1/souls', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({
                        user_id: userID,
                        name: name,
                        mbti_type: mbti
                    })
                });
                appendLog('[souls/create] 已创建 soul_id=' + String(out.soul_id || '') + ' name=' + String(out.name || name) + ' mbti=' + String(out.mbti_type || mbti));
                setConnectionStatus('灵魂创建成功，请点击“选择并绑定”', false);
                if (view.newSoulNameInput) {
                    view.newSoulNameInput.value = '';
                }
                await refreshSouls(String(out.soul_id || '').trim());
            } catch (err) {
                const message = err && err.message ? err.message : String(err);
                setConnectionStatus('创建灵魂失败：' + message, true);
                appendLog('[souls/create] 失败: ' + message);
            }
        }

        function matchConfiguredIntent(text) {
            const normalized = normalizeTriggerText(text);
            if (!normalized) return null;

            for (let i = 0; i < INTENT_CONFIGS.length; i++) {
                const cfg = INTENT_CONFIGS[i];
                for (let j = 0; j < cfg.triggers.length; j++) {
                    const key = normalizeTriggerText(cfg.triggers[j]);
                    if (key && normalized.indexOf(key) >= 0) {
                        return cfg;
                    }
                }
            }
            return null;
        }

        function runIntentExpression(intentCfg) {
            const face = global.robotFace;
            if (!face || typeof face.setEmotion !== 'function') {
                appendLog('[local_intent] 命中 ' + intentCfg.label + '，但表情引擎未就绪');
                return;
            }

            const holdMs = randomDurationMs(intentCfg.minMs, intentCfg.maxMs);
            if (faceRestoreTimer) {
                clearTimeout(faceRestoreTimer);
                faceRestoreTimer = null;
            }

            intentOverrideUntil = Date.now() + holdMs;
            face.setEmotion(intentCfg.emotion);
            appendLog('[local_intent] 命中 ' + intentCfg.label + ' -> ' + intentCfg.emotion + '，' + (holdMs / 1000).toFixed(1) + 's 后恢复 Normal');

            faceRestoreTimer = global.setTimeout(function () {
                intentOverrideUntil = 0;
                face.setEmotion('Normal');
                appendLog('[local_intent] ' + intentCfg.label + ' 执行结束，恢复 Normal');
            }, holdMs);
        }

        function appendTurn(role, content, executedSkills) {
            const turns = getTurns(activeSessionId);
            turns.push({
                role: role,
                content: String(content || ''),
                timeText: nowTimeText(),
                executedSkills: Array.isArray(executedSkills) ? executedSkills.slice() : []
            });
            if (turns.length > 120) {
                turns.splice(0, turns.length - 120);
            }
            renderConversation();
        }

        async function checkHealth() {
            try {
                const out = await fetchJSON('/healthz');
                if (out && out.ok) {
                    setConnectionStatus('已连接 soul-server /healthz', false);
                    appendLog('[healthz] soul-server ok=true');
                } else {
                    setConnectionStatus('健康检查返回异常', true);
                    appendLog('[healthz] 返回异常: ' + JSON.stringify(out));
                }
            } catch (err) {
                const message = err && err.message ? err.message : String(err);
                setConnectionStatus('连接失败：' + message, true);
                appendLog('[healthz] 连接失败: ' + message);
            }
        }

        function persistConfig() {
            safeSetStorage(TERMINAL_ID_STORAGE_KEY, view.terminalIdInput.value.trim());
            safeSetStorage(USER_ID_STORAGE_KEY, view.userIdInput.value.trim());
            safeSetStorage(SOUL_HINT_STORAGE_KEY, view.soulHintInput.value.trim());
        }

        async function sendChat() {
            const text = view.chatInput.value.trim();
            if (!text) return;

            const terminalId = getTerminalId();
            if (!terminalId) {
                setConnectionStatus('发送失败：terminal_id 不能为空', true);
                appendLog('[chat] terminal_id 为空，已拦截请求');
                return;
            }
            if (!currentBoundSoulID) {
                setConnectionStatus('发送失败：请先创建并绑定灵魂', true);
                appendLog('[chat] 当前未绑定灵魂，已拦截请求');
                return;
            }

            view.sendBtn.disabled = true;
            view.sendBtn.classList.add('opacity-70', 'cursor-not-allowed');

            const userId = getUserId();
            const soulHint = currentBoundSoulID;
            const inputId = 'in-' + Date.now();
            const ts = nowISO();

            const payload = {
                session_id: activeSessionId,
                terminal_id: terminalId,
                inputs: [
                    {
                        input_id: inputId,
                        type: 'keyboard_text',
                        source: 'keyboard',
                        ts: ts,
                        text: text
                    }
                ]
            };

            if (userId) payload.user_id = userId;
            if (soulHint) payload.soul_hint = soulHint;

            appendTurn('user', text, []);
            appendLog('[chat][session:' + activeSessionId + '] user: ' + text);
            view.chatInput.value = '';

            const matched = matchConfiguredIntent(text);
            if (matched) {
                runIntentExpression(matched);
            }

            try {
                const out = await fetchJSON('/v1/chat', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify(payload)
                });

                const reply = String(out.reply || '');
                const executedSkills = Array.isArray(out.executed_skills) ? out.executed_skills : [];
                appendTurn('assistant', reply, executedSkills);

                const decision = out.intent_decision ? String(out.intent_decision) : '-';
                const execMode = out.exec_mode ? String(out.exec_mode) : '-';
                const execProbability = typeof out.exec_probability === 'number' ? out.exec_probability.toFixed(3) : '-';
                appendLog('[chat][session:' + activeSessionId + '] assistant: ' + (reply || '<NO_REPLY>') + ' | skills=' + executedSkills.join(',') + ' | intent=' + decision + ' | mode=' + execMode + ' | prob=' + execProbability);

                setConnectionStatus('已连接并完成一轮对话', false);
            } catch (err) {
                const message = err && err.message ? err.message : String(err);
                appendLog('[chat][session:' + activeSessionId + '] failed: ' + message);
                setConnectionStatus('发送失败：' + message, true);

                if (message.indexOf('soul selection is required before chat') >= 0) {
                    currentBoundSoulID = '';
                    if (view.soulHintInput) {
                        view.soulHintInput.value = '';
                    }
                    updateSelectedSoulInfo();
                    appendLog('[hint] 需要先在 soul-server 绑定 soul：POST /v1/souls/select');
                }
            } finally {
                view.sendBtn.disabled = false;
                view.sendBtn.classList.remove('opacity-70', 'cursor-not-allowed');
            }
        }

        function newSession() {
            activeSessionId = generateSessionId();
            getTurns(activeSessionId);
            updateSessionInfo();
            renderConversation();
            appendLog('[session] new local session=' + activeSessionId);
        }

        function bindEvents() {
            view.connectStateBtn.addEventListener('click', function () {
                persistConfig();
                checkHealth();
                refreshSouls();
            });

            view.newSessionBtn.addEventListener('click', function () {
                persistConfig();
                newSession();
            });

            view.sendBtn.addEventListener('click', function () {
                persistConfig();
                sendChat();
            });

            view.chatInput.addEventListener('keydown', function (ev) {
                if (ev.key === 'Enter' && !ev.shiftKey) {
                    ev.preventDefault();
                    persistConfig();
                    sendChat();
                }
            });

            if (view.refreshSoulsBtn) {
                view.refreshSoulsBtn.addEventListener('click', function () {
                    persistConfig();
                    refreshSouls();
                });
            }

            if (view.bindSoulBtn) {
                view.bindSoulBtn.addEventListener('click', function () {
                    persistConfig();
                    bindSelectedSoul();
                });
            }

            if (view.createSoulBtn) {
                view.createSoulBtn.addEventListener('click', function () {
                    persistConfig();
                    createSoul();
                });
            }

            if (view.soulSelect) {
                view.soulSelect.addEventListener('change', function () {
                    updateSelectedSoulInfo();
                });
            }

            if (view.userIdInput) {
                view.userIdInput.addEventListener('change', function () {
                    currentBoundSoulID = '';
                    if (view.soulHintInput) {
                        view.soulHintInput.value = '';
                    }
                    persistConfig();
                    refreshSouls();
                });
            }
        }

        function restoreConfig() {
            let storedTerminal = safeGetStorage(TERMINAL_ID_STORAGE_KEY, 'web-robot-01').trim();
            if (!storedTerminal || storedTerminal === 'terminal-debug-01') {
                storedTerminal = 'web-robot-01';
            }
            let storedUser = safeGetStorage(USER_ID_STORAGE_KEY, 'web_user_20260223_flow').trim();
            if (!storedUser || storedUser === 'demo-user') {
                storedUser = 'web_user_20260223_flow';
            }
            const storedSoulHint = safeGetStorage(SOUL_HINT_STORAGE_KEY, '');

            view.terminalIdInput.value = storedTerminal;
            view.userIdInput.value = storedUser;
            view.soulHintInput.value = storedSoulHint;
            currentBoundSoulID = storedSoulHint.trim();
        }

        restoreConfig();
        renderIntentConfigs();
        bindEvents();
        updateSessionInfo();
        updateSelectedSoulInfo();
        renderConversation();
        renderLogs();

        const face = global.robotFace;
        if (face && typeof face.setEmotion === 'function' && Date.now() >= intentOverrideUntil) {
            face.setEmotion('Normal');
        }

        checkHealth();
        refreshSouls(currentBoundSoulID);
    }

    global.WebRobotBody = global.WebRobotBody || {};
    global.WebRobotBody.initSoulBridge = initSoulBridge;
})(window, document);
