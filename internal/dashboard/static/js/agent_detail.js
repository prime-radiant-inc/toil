// agent_detail.js — bootstraps the per-execution transcripts on the
// diagnostic agent-detail page. Each transcript is embedded as inert
// JSON in a <script type="application/json"> tag; the matching mount
// point carries data-transcript-target with the script id.
(function () {
  function init() {
    if (!window.TranscriptView) {
      // The defer-loaded transcript_view.js may not be ready yet on a
      // truly cold page; retry shortly.
      setTimeout(init, 30);
      return;
    }
    document.querySelectorAll('[data-transcript-target]').forEach((mount) => {
      const sourceID = mount.getAttribute('data-transcript-target');
      const source = document.getElementById(sourceID);
      if (!source) return;
      let transcript;
      try {
        transcript = JSON.parse(source.textContent);
      } catch (err) {
        mount.textContent = 'Failed to parse transcript JSON: ' + err.message;
        return;
      }
      mount.appendChild(window.TranscriptView.render(transcript));
    });
  }
  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();
