(function (global, document) {
    function init() {
        const api = global.WebRobotBody || {};
        if (typeof api.initLogPanel === 'function') {
            api.initLogPanel();
        }
        if (typeof api.initRobotFacePanel === 'function') {
            api.initRobotFacePanel();
        }
        if (typeof api.initSoulBridge === 'function') {
            api.initSoulBridge();
        }
    }

    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', init);
    } else {
        init();
    }
})(window, document);
