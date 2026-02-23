(function (global) {
    function initLogPanel() {
        // 显示/隐藏日志
        const toggleLogBtn = document.getElementById('toggleLogBtn');
        const closeLogBtn = document.getElementById('closeLogBtn');
        const logArea = document.getElementById('logArea');
        const logResizeHandle = document.getElementById('logResizeHandle');

        function toggleLog() {
            logArea.classList.toggle('hidden');
        }

        toggleLogBtn.addEventListener('click', toggleLog);
        closeLogBtn.addEventListener('click', toggleLog);

        // 底部日志栏高度拖动
        const LOG_MIN_H = 80;
        const LOG_MAX_H = () => Math.min(window.innerHeight * 0.8, 600);

        logResizeHandle.addEventListener('mousedown', function (e) {
            e.preventDefault();
            const startY = e.clientY;
            const startH = logArea.offsetHeight;

            function onMove(e) {
                const dy = startY - e.clientY;
                let h = startH + dy;
                h = Math.max(LOG_MIN_H, Math.min(LOG_MAX_H(), h));
                logArea.style.height = h + 'px';
            }

            function onUp() {
                document.removeEventListener('mousemove', onMove);
                document.removeEventListener('mouseup', onUp);
                document.body.style.cursor = '';
                document.body.style.userSelect = '';
            }

            document.body.style.cursor = 'ns-resize';
            document.body.style.userSelect = 'none';
            document.addEventListener('mousemove', onMove);
            document.addEventListener('mouseup', onUp);
        });
    }

    global.WebRobotBody = global.WebRobotBody || {};
    global.WebRobotBody.initLogPanel = initLogPanel;
})(window);
