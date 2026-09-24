<script>
  import { onDestroy, onMount, tick } from 'svelte'
  import { AddBulkCustomRule, AddCustomRule, DeleteCustomRule, GhostHideToTray, LoadRules, RedactText, ResetRules, RestoreGhostModeFromSettings, SaveRules, SetGhostModeEnabled, SimulatePasteAfterDelay, StartClipboardTimer, StartGhostWatcher, ToggleSatelliteMode } from '../wailsjs/go/main/App.js'
  import { ClipboardSetText, EventsOn, Quit, WindowMinimise, WindowShow, WindowToggleMaximise } from '../wailsjs/runtime/runtime.js'
  import brandLogo from './assets/images/SR_LOGO3.png'
  import About from './components/About.svelte'

  const AUTO_COPY_STORAGE_KEY = 'siloRedact_autoCopy'
  const TIMED_CLIPBOARD_WIPE_STORAGE_KEY = 'siloRedact_timedClipboardWipe'
  const AUTO_PASTE_STORAGE_KEY = 'siloRedact_autoPaste'
  const GHOST_MODE_STORAGE_KEY = 'siloRedact_ghostMode'
  const DEBUG_WARNING_KEY = 'siloRedact_debugWarningSeen'

  const AI_PROMPT_TEMPLATES = [
    { id: '', label: 'None', text: '' },
    { id: 'security', label: 'Security', text: 'Analyze these logs for IOCs or lateral movement:' },
    { id: 'devops', label: 'DevOps', text: 'Explain this error and suggest a fix:' },
    { id: 'general', label: 'General', text: 'Summarize these logs and highlight critical events:' }
  ]
  let selectedAiPromptTemplate = ''

  /** High-priority keywords for Smart Trim (case-insensitive match). */
  const SMART_TRIM_KEYWORDS = ['error', 'critical', 'fatal', 'exception', 'fail', '404', '500']
  const SMART_TRIM_CONTEXT = 2
  const SMART_TRIM_OMITTED_PLACEHOLDER = '... [Lines Omitted] ...'
  let smartTrimEnabled = false

  function applySmartTrim(text) {
    if (!text || !text.trim()) return text || ''
    const lines = text.split(/\r?\n/)
    const keepOrVisible = new Set()

    for (let i = 0; i < lines.length; i++) {
      const lineLower = lines[i].toLowerCase()
      if (SMART_TRIM_KEYWORDS.some((kw) => lineLower.includes(kw))) {
        for (let j = Math.max(0, i - SMART_TRIM_CONTEXT); j <= Math.min(lines.length - 1, i + SMART_TRIM_CONTEXT); j++) {
          keepOrVisible.add(j)
        }
      }
    }

    if (keepOrVisible.size === 0) return text

    const result = []
    let i = 0
    while (i < lines.length) {
      if (keepOrVisible.has(i)) {
        result.push(lines[i])
        i++
      } else {
        while (i < lines.length && !keepOrVisible.has(i)) i++
        result.push(SMART_TRIM_OMITTED_PLACEHOLDER)
      }
    }
    return result.join('\n')
  }

  $: displayedSanitizedOutput = (() => {
    const t = AI_PROMPT_TEMPLATES.find((r) => r.id === selectedAiPromptTemplate)
    const prefix = t?.text ? t.text + '\n\n' : ''
    const base = smartTrimEnabled ? applySmartTrim(sanitizedOutput || '') : (sanitizedOutput || '')
    return prefix + base
  })()

  let rawInput = ''
  let sanitizedOutput = ''
  let copyStatus = ''
  let isAutoCopyEnabled = false
  let autoCopiedFlash = false
  let copyCheckFlash = false
  let lastRedactedInput = ''

  let debugMode = false
  let showDebugWarning = false
  let redactionEvents = [] // { ruleName, originalText, replacement, startIndex, endIndex }[]

  let activeTab = 'Workspace' // 'Workspace' | 'Rules Library'
  let rules = []
  let rulesLoading = false
  let rulesError = ''
  let isLoadingRules = true
  let appInitialized = false

  let ruleSearch = ''
  let ruleCategoryFilter = 'All' // All | Cloud & Auth | Network | Sensitive Data | System | Custom
  let filteredRules = []
  let filteredCategorizedCount = 0

  let quickAddText = ''
  let quickAddReplacement = ''
  let quickAddSeverity = 'Medium'
  let quickAddCategory = 'System'
  let quickAddStatus = ''
  let showAddRuleModal = false
  let bulkListText = ''
  let bulkMaskType = 'host'
  let bulkCategory = 'System'
  let bulkSeverity = 'Medium'
  let bulkStatus = ''
  let bulkInProgress = false
  let bulkDropZoneEl = null
  let isBulkDragging = false

  let showWarningModal = false
  let pendingToggleRuleId = ''
  let pendingToggleNextValue = false
  let layoutKey = 0

  let showResetModal = false
  let resetInProgress = false
  let showAboutModal = false
  let showSettingsModal = false
  const APP_VERSION = '1.0.0'
  let rawInputEl
  let dropZoneEl
  let sanitizedOutputEl
  let isDraggingOver = false
  let dropToastMessage = ''

  let timedClipboardWipeEnabled = false
  let clipboardWipeSecondsLeft = 0
  let clipboardWipeIntervalId = null

  let ghostModeEnabled = false
  let ghostToast = false
  let showNotificationUnsubscribe = null
  let trayRequestShowUnsubscribe = null
  let trayRequestQuitUnsubscribe = null
  let wheelHandler = null

  // Auto Paste (simulate Ctrl+V after redaction): UI removed; logic kept for potential re-use
  let autoPasteEnabled = false
  let isSatellite = false
  let satelliteShowRawInput = true

  let searchOpen = false
  let searchQuery = ''
  let searchInputEl
  let rawHighlightEl
  let safeHighlightEl

  let zoomLevel = 100
  let showZoomGhost = false
  let zoomGhostFadeOut = false
  let zoomGhostTimeout = null
  const ZOOM_MIN = 50
  const ZOOM_MAX = 200
  const ZOOM_STEP = 10
  const ZOOM_GHOST_FADE_MS = 500

  function showZoomIndicator() {
    showZoomGhost = true
    zoomGhostFadeOut = false
    if (zoomGhostTimeout) clearTimeout(zoomGhostTimeout)
    zoomGhostTimeout = setTimeout(() => {
      zoomGhostFadeOut = true
      zoomGhostTimeout = setTimeout(() => {
        showZoomGhost = false
        zoomGhostFadeOut = false
        zoomGhostTimeout = null
      }, ZOOM_GHOST_FADE_MS)
    }, 2500)
  }

  function adjustZoom(delta) {
    zoomLevel = Math.min(ZOOM_MAX, Math.max(ZOOM_MIN, zoomLevel + delta))
    showZoomIndicator()
  }

  const ALLOWED_DROP_EXTENSIONS = ['.txt', '.log', '.csv', '.json']

  function isAllowedDropFile(file) {
    const name = (file?.name ?? '').toLowerCase()
    return ALLOWED_DROP_EXTENSIONS.some((ext) => name.endsWith(ext))
  }

  function handleDragEnter(e) {
    e.preventDefault()
    e.stopPropagation()
    isDraggingOver = true
  }

  function handleDragOver(e) {
    e.preventDefault()
    e.stopPropagation()
    if (e.dataTransfer) e.dataTransfer.dropEffect = 'copy'
  }

  function handleDragLeave(e) {
    e.preventDefault()
    e.stopPropagation()
    if (!dropZoneEl || !dropZoneEl.contains(e.relatedTarget)) {
      isDraggingOver = false
    }
  }

  function readFileAsText(file) {
    return new Promise((resolve, reject) => {
      const reader = new FileReader()
      reader.onload = (ev) => {
        const result = ev.target?.result
        resolve(typeof result === 'string' ? result : '')
      }
      reader.onerror = () => reject(new Error('Could not read file'))
      reader.readAsText(file)
    })
  }

  async function handleDrop(e) {
    e.preventDefault()
    e.stopPropagation()
    isDraggingOver = false
    dropToastMessage = ''
    const fileList = e.dataTransfer?.files
    if (!fileList?.length) return
    const files = Array.from(fileList).filter((f) => isAllowedDropFile(f))
    if (files.length === 0) {
      dropToastMessage = 'Only .txt, .log, .csv, or .json files are allowed.'
      setTimeout(() => (dropToastMessage = ''), 3500)
      return
    }
    if (files.length < fileList.length) {
      dropToastMessage = 'Some files were skipped (only .txt, .log, .csv, .json allowed).'
      setTimeout(() => (dropToastMessage = ''), 3500)
    }
    const sourceHeader = (name) => `--- SOURCE: ${name} ---\n`
    const separatorWithHeader = (name) => `\n\n--- SOURCE: ${name} ---\n`
    redactionEvents = []
    let appendedRaw = rawInput
    let appendedSanitized = sanitizedOutput
    for (const file of files) {
      try {
        const text = await readFileAsText(file)
        const result = await RedactText(text)
        const redacted = result?.redactedText ?? (typeof result === 'string' ? result : '')
        const hasExisting = appendedRaw.length > 0 || appendedSanitized.length > 0
        if (hasExisting) {
          appendedRaw += separatorWithHeader(file.name)
          appendedSanitized += separatorWithHeader(file.name)
        } else {
          appendedRaw += sourceHeader(file.name)
          appendedSanitized += sourceHeader(file.name)
        }
        appendedRaw += text
        appendedSanitized += redacted
      } catch (err) {
        dropToastMessage = `Could not process ${file.name}.`
        setTimeout(() => (dropToastMessage = ''), 3500)
      }
    }
    rawInput = appendedRaw
    sanitizedOutput = appendedSanitized
    await tick()
    if (sanitizedOutputEl) {
      sanitizedOutputEl.scrollTop = sanitizedOutputEl.scrollHeight
    }
  }

  function downloadOutput() {
    if (!displayedSanitizedOutput) return
    const blob = new Blob([displayedSanitizedOutput], { type: 'text/plain' })
    const filename = 'OneBoardSafePaste_export.txt'
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = filename
    a.click()
    URL.revokeObjectURL(url)
  }

  const CLEAR_CONFIRM_LIMIT = 5000

  function clearAll() {
    const total = (rawInput?.length ?? 0) + (sanitizedOutput?.length ?? 0)
    if (total > CLEAR_CONFIRM_LIMIT) {
      if (!confirm(`Clear all? This will remove ${total.toLocaleString()} characters.`)) return
    }
    rawInput = ''
    sanitizedOutput = ''
    redactionEvents = []
  }

  function normCategory(value) {
    return (value ?? '').toString().trim().toLowerCase()
  }

  function normSeverity(value) {
    const v = (value ?? '').toString().trim()
    if (/^critical$/i.test(v)) return 'Critical'
    if (/^high$/i.test(v)) return 'High'
    if (/^medium$/i.test(v)) return 'Medium'
    return v || 'Medium'
  }

  function rulesByCategory(category) {
    const target = normCategory(category)
    return rules.filter((r) => normCategory(r.category) === target)
  }

  function matchesSearch(rule, query) {
    const q = (query ?? '').trim().toLowerCase()
    if (!q) return true
    const hay = `${rule?.name ?? ''} ${rule?.description ?? ''}`.toLowerCase()
    return hay.includes(q)
  }

  function matchesCategory(rule, filter) {
    const f = normCategory(filter)
    if (!f || f === 'all') return true
    if (f === 'custom') return (rule?.source ?? '').toString().toLowerCase() === 'custom'
    return normCategory(rule?.category) === f
  }

  $: filteredRules = (rules ?? []).filter(
    (r) => matchesCategory(r, ruleCategoryFilter) && matchesSearch(r, ruleSearch)
  )

  const categoryOrder = ['Cloud & Auth', 'Network', 'Sensitive Data', 'System']
  $: groupedFilteredRules = categoryOrder
    .map((cat) => ({ category: cat, rules: filteredRules.filter((r) => normCategory(r.category) === normCategory(cat)) }))
    .filter((g) => g.rules.length > 0)

  function filteredByCategory(category) {
    const target = normCategory(category)
    return filteredRules.filter((r) => normCategory(r.category) === target)
  }

  $: filteredCategorizedCount =
    filteredByCategory('Cloud & Auth').length +
    filteredByCategory('Network').length +
    filteredByCategory('Sensitive Data').length +
    filteredByCategory('System').length +
    filteredByCategory('Custom').length

  $: rulesList = Array.isArray(rules) ? rules : []
  $: activeRuleCount = rulesList.filter((r) => r && r.isEnabled).length
  $: customRuleCount = rulesList.filter((r) => r && (r?.source ?? '').toString().toLowerCase() === 'custom').length

  let debounceTimer
  function debounce(fn, delayMs = 300) {
    return (...args) => {
      clearTimeout(debounceTimer)
      debounceTimer = setTimeout(() => fn(...args), delayMs)
    }
  }

  async function redactNow(text) {
    copyStatus = ''
    redactionEvents = []
    const result = await RedactText(text ?? '', debugMode)
    sanitizedOutput = result?.redactedText ?? (typeof result === 'string' ? result : '')
    redactionEvents = result?.events ?? []
  }

  function onDebugModeChange() {
    if (debugMode) {
      try {
        showDebugWarning = localStorage.getItem(DEBUG_WARNING_KEY) !== 'true'
      } catch (_) {
        showDebugWarning = true
      }
    } else {
      showDebugWarning = false
    }
    void redactNow(rawInput)
  }

  function dismissDebugWarning() {
    showDebugWarning = false
    try {
      localStorage.setItem(DEBUG_WARNING_KEY, 'true')
    } catch (_) {}
  }

  // Segments for debug output: mix of plain text and redaction spans with tooltips
  $: outputSegments = (() => {
    if (!debugMode || !sanitizedOutput || !redactionEvents?.length) return null
    const text = sanitizedOutput
    const events = redactionEvents
    const segs = []
    let pos = 0
    for (const e of events) {
      if (e.startIndex > pos) {
        segs.push({ type: 'text', value: text.slice(pos, e.startIndex) })
      }
      segs.push({ type: 'redaction', value: e.replacement || '[REDACTED]', ruleName: e.ruleName || 'Unknown' })
      pos = e.endIndex ?? (e.startIndex + (e.replacement || '').length)
    }
    if (pos < text.length) {
      segs.push({ type: 'text', value: text.slice(pos) })
    }
    return segs
  })()

  const runRedaction = debounce(async (text) => {
    await redactNow(text)
  }, 300)

  $: runRedaction(rawInput)

  // Ghost Mode: only process notifications when we have rules (ghost-content-detected safety).
  function onShowNotification(message) {
    if (rules.length === 0) return
    if (typeof message === 'string' && message) {
      ghostToast = true
      setTimeout(() => (ghostToast = false), 2500)
      if (typeof Notification !== 'undefined' && Notification.permission === 'granted') {
        new Notification('OneBoard Safe Paste', { body: message })
      }
    }
  }
  showNotificationUnsubscribe = EventsOn('show-notification', onShowNotification)

  function onGhostModeSet(enabled) {
    ghostModeEnabled = !!enabled
    try {
      localStorage.setItem(GHOST_MODE_STORAGE_KEY, String(ghostModeEnabled))
    } catch (_) {}
  }
  const ghostModeSetUnsubscribe = EventsOn('ghost-mode-set', onGhostModeSet)

  async function onTrayRequestShow() {
    ghostModeEnabled = false
    try {
      localStorage.setItem(GHOST_MODE_STORAGE_KEY, 'false')
    } catch (_) {}
    try {
      await SetGhostModeEnabled(false)
    } catch (_) {}
    try {
      WindowShow()
    } catch (_) {}
  }

  trayRequestShowUnsubscribe = EventsOn('tray-request-show', () => {
    void onTrayRequestShow()
  })
  trayRequestQuitUnsubscribe = EventsOn('tray-request-quit', () => {
    Quit()
  })

  let autoCopyDebounceTimer
  let autoCopyFlashTimer
  let copyCheckTimer
  $: if ((isAutoCopyEnabled || autoPasteEnabled) && sanitizedOutput && sanitizedOutput.trim() !== '') {
    clearTimeout(autoCopyDebounceTimer)
    autoCopyDebounceTimer = setTimeout(async () => {
      if (!isAutoCopyEnabled && !autoPasteEnabled) return
      const next = sanitizedOutput
      if (!next || next.trim() === '') return
      if (next === lastRedactedInput) return
      try {
        await ClipboardSetText(next)
        lastRedactedInput = next

        if (autoPasteEnabled) {
          SimulatePasteAfterDelay(500)
        }

        if (isAutoCopyEnabled) {
          if (autoCopyFlashTimer) clearTimeout(autoCopyFlashTimer)
          autoCopiedFlash = true
          autoCopyFlashTimer = setTimeout(() => {
            autoCopiedFlash = false
            autoCopyFlashTimer = null
          }, 2200)

          if (copyCheckTimer) clearTimeout(copyCheckTimer)
          copyCheckFlash = true
          copyCheckTimer = setTimeout(() => {
            copyCheckFlash = false
            copyCheckTimer = null
          }, 1500)
        }

        scheduleClipboardWipe(next)
      } catch (_) {}
    }, 500)
  }

  async function refreshRules() {
    rulesLoading = true
    rulesError = ''
    try {
      const raw = await LoadRules()
      rules = raw || []
    } catch (e) {
      rulesError = e?.message ?? 'Failed to load rules'
      rules = []
    } finally {
      rulesLoading = false
    }
  }

  async function doPostRulesInit() {
    try {
      isAutoCopyEnabled = localStorage.getItem(AUTO_COPY_STORAGE_KEY) === 'true'
      timedClipboardWipeEnabled = localStorage.getItem(TIMED_CLIPBOARD_WIPE_STORAGE_KEY) === 'true'
      autoPasteEnabled = localStorage.getItem(AUTO_PASTE_STORAGE_KEY) === 'true'
      ghostModeEnabled = localStorage.getItem(GHOST_MODE_STORAGE_KEY) === 'true'
    } catch (_) {}
    await RestoreGhostModeFromSettings(ghostModeEnabled)
    StartGhostWatcher()
    if (typeof Notification !== 'undefined' && Notification.permission === 'default') {
      Notification.requestPermission()
    }
    await redactNow(rawInput)
    setTimeout(() => rawInputEl?.focus(), 100)
  }

  async function loadDataSequentially() {
    isLoadingRules = true
    try {
      await refreshRules()
      await doPostRulesInit()
    } finally {
      isLoadingRules = false
    }
  }

  onMount(() => {
    appInitialized = true
    wheelHandler = (e) => {
      if (e.ctrlKey || e.metaKey) {
        e.preventDefault()
        adjustZoom(e.deltaY > 0 ? -ZOOM_STEP : ZOOM_STEP)
      }
    }
    window.addEventListener('wheel', wheelHandler, { passive: false })
    loadDataSequentially()
  })

  function persistAutoCopy() {
    try {
      localStorage.setItem(AUTO_COPY_STORAGE_KEY, String(isAutoCopyEnabled))
    } catch (_) {}
  }

  function persistTimedClipboardWipe() {
    try {
      localStorage.setItem(TIMED_CLIPBOARD_WIPE_STORAGE_KEY, String(timedClipboardWipeEnabled))
    } catch (_) {}
  }

  function persistAutoPaste() {
    try {
      localStorage.setItem(AUTO_PASTE_STORAGE_KEY, String(autoPasteEnabled))
    } catch (_) {}
  }

  function persistGhostMode() {
    try {
      SetGhostModeEnabled(ghostModeEnabled)
      localStorage.setItem(GHOST_MODE_STORAGE_KEY, String(ghostModeEnabled))
    } catch (_) {}
  }

  const AUTO_PASTE_WARNING =
    'Auto Paste will simulate Ctrl+V (Cmd+V on Mac) about 500ms after each redaction. Use with Auto-Copy: focus your target window (e.g. AI chat) within that time. Continue?'

  async function toggleSatellite() {
    isSatellite = !isSatellite
    try {
      await ToggleSatelliteMode(isSatellite)
    } catch (_) {
      isSatellite = !isSatellite
    }
  }

  function handleWindowClose() {
    if (ghostModeEnabled) {
      void GhostHideToTray()
    } else {
      Quit()
    }
  }

  function onAutoPasteChange() {
    if (autoPasteEnabled) {
      if (!confirm(AUTO_PASTE_WARNING)) {
        autoPasteEnabled = false
        return
      }
    }
    persistAutoPaste()
  }

  onDestroy(() => {
    if (wheelHandler) window.removeEventListener('wheel', wheelHandler)
    if (clipboardWipeIntervalId) clearInterval(clipboardWipeIntervalId)
    if (showNotificationUnsubscribe) showNotificationUnsubscribe()
    if (ghostModeSetUnsubscribe) ghostModeSetUnsubscribe()
    if (trayRequestShowUnsubscribe) trayRequestShowUnsubscribe()
    if (trayRequestQuitUnsubscribe) trayRequestQuitUnsubscribe()
  })

  function requestToggleRule(ruleId, nextValue) {
    const rule = rules.find((r) => r.id === ruleId)
    if (!rule) return

    if ((rule?.source ?? '').toString().toLowerCase() !== 'custom' && nextValue === false) {
      pendingToggleRuleId = ruleId
      pendingToggleNextValue = nextValue
      showWarningModal = true
      // Force UI to keep the toggle ON until confirmed.
      rules = rules.map((r) => (r.id === ruleId ? { ...r, isEnabled: true } : r))
      layoutKey += 1
      return
    }

    void applyToggleRule(ruleId, nextValue)
  }

  async function applyToggleRule(ruleId, nextValue) {
    const updated = rules.map((r) => (r.id === ruleId ? { ...r, isEnabled: nextValue } : r))
    rules = updated
    try {
      await SaveRules(updated)
    } catch (e) {
      rulesError = e?.message ?? 'Failed to save rules'
    }
    await redactNow(rawInput)
  }

  async function confirmRiskAndDisable() {
    showWarningModal = false
    const ruleId = pendingToggleRuleId
    const nextValue = pendingToggleNextValue
    pendingToggleRuleId = ''
    pendingToggleNextValue = false
    if (!ruleId) return
    await applyToggleRule(ruleId, nextValue)
  }

  function cancelRiskToggle() {
    if (pendingToggleRuleId) {
      rules = rules.map((r) => (r.id === pendingToggleRuleId ? { ...r, isEnabled: true } : r))
      layoutKey += 1
    }
    showWarningModal = false
    pendingToggleRuleId = ''
    pendingToggleNextValue = false
  }

  async function addQuickCustom() {
    const text = (quickAddText ?? '').trim()
    if (!text) return
    quickAddStatus = ''
    try {
      await AddCustomRule(text, true, quickAddReplacement ?? '', quickAddSeverity ?? 'Medium', quickAddCategory ?? 'System')
      quickAddText = ''
      quickAddReplacement = ''
      quickAddStatus = 'Added'
      showAddRuleModal = false
      await refreshRules()
      await redactNow(rawInput)
      setTimeout(() => (quickAddStatus = ''), 1200)
    } catch (e) {
      quickAddStatus = e?.message ?? 'Add failed'
      setTimeout(() => (quickAddStatus = ''), 1500)
    }
  }

  function openAddRuleModal() {
    quickAddStatus = ''
    showAddRuleModal = true
  }

  function closeAddRuleModal() {
    showAddRuleModal = false
  }

  function openAboutModal() {
    showAboutModal = true
  }

  function closeAboutModal() {
    showAboutModal = false
  }

  function openSettingsModal() {
    showSettingsModal = true
  }

  function closeSettingsModal() {
    showSettingsModal = false
  }

  function handleBulkDragOver(e) {
    e.preventDefault()
    e.stopPropagation()
    if (e.dataTransfer) e.dataTransfer.dropEffect = 'copy'
  }

  function handleBulkDragEnter(e) {
    e.preventDefault()
    e.stopPropagation()
    isBulkDragging = true
  }

  function handleBulkDragLeave(e) {
    e.preventDefault()
    e.stopPropagation()
    if (!bulkDropZoneEl || !bulkDropZoneEl.contains(e.relatedTarget)) {
      isBulkDragging = false
    }
  }

  async function handleBulkDrop(e) {
    e.preventDefault()
    e.stopPropagation()
    isBulkDragging = false
    bulkStatus = ''
    const files = e.dataTransfer?.files
    if (!files?.length) return
    const file = files[0]
    if (!isAllowedDropFile(file)) {
      bulkStatus = 'Use a .txt, .log, or .csv file for the list.'
      return
    }
    try {
      const text = await readFileAsText(file)
      bulkListText = text
      bulkStatus = `Loaded ${file.name}. Choose options and click "Add bulk rule".`
    } catch (err) {
      bulkStatus = 'Could not read the file.'
    }
  }

  async function addBulkRules() {
    const text = (bulkListText ?? '').trim()
    if (!text) {
      bulkStatus = 'Paste a list first (one per line or comma-separated).'
      return
    }
    bulkStatus = ''
    bulkInProgress = true
    try {
      await AddBulkCustomRule(text, bulkMaskType, bulkCategory ?? 'System', bulkSeverity ?? 'Medium')
      bulkListText = ''
      bulkStatus = 'Bulk rule added. Refresh or switch tab to see it.'
      await refreshRules()
    } catch (e) {
      bulkStatus = e?.message ?? 'Failed to add bulk rule.'
    } finally {
      bulkInProgress = false
    }
  }

  async function copyToClipboard() {
    try {
      const text = displayedSanitizedOutput
      await ClipboardSetText(text)
      copyStatus = 'Copied!'
      setTimeout(() => (copyStatus = ''), 2000)
      scheduleClipboardWipe(text)
    } catch {
      copyStatus = 'Copy failed'
      setTimeout(() => (copyStatus = ''), 2000)
    }
  }

  async function scheduleClipboardWipe(text) {
    if (!timedClipboardWipeEnabled || !text) return
    void StartClipboardTimer(text).catch(() => {})
    startClipboardWipeCountdown()
  }

  function startClipboardWipeCountdown() {
    if (clipboardWipeIntervalId) {
      clearInterval(clipboardWipeIntervalId)
      clipboardWipeIntervalId = null
    }
    clipboardWipeSecondsLeft = 30
    clipboardWipeIntervalId = setInterval(() => {
      if (clipboardWipeSecondsLeft <= 1) {
        clipboardWipeSecondsLeft = 0
        if (clipboardWipeIntervalId) {
          clearInterval(clipboardWipeIntervalId)
          clipboardWipeIntervalId = null
        }
        return
      }
      clipboardWipeSecondsLeft = clipboardWipeSecondsLeft - 1
    }, 1000)
  }

  let saveRulesTimer
  function scheduleSaveRules(updatedRules) {
    rules = updatedRules
    clearTimeout(saveRulesTimer)
    saveRulesTimer = setTimeout(async () => {
      try {
        await SaveRules(updatedRules)
        // Ensure Go has the latest rules before redacting.
        await redactNow(rawInput)
      } catch (e) {
        rulesError = e?.message ?? 'Failed to save rules'
      }
    }, 250)
  }

  function updateRule(ruleId, patch) {
    const updated = rules.map((r) => (r.id === ruleId ? { ...r, ...patch } : r))
    scheduleSaveRules(updated)
    void redactNow(rawInput)
  }

  async function deleteCustomRule(ruleId) {
    try {
      await DeleteCustomRule(ruleId)
      await refreshRules()
      await redactNow(rawInput)
    } catch (e) {
      rulesError = e?.message ?? 'Failed to delete rule'
    }
  }

  function openResetModal() {
    showResetModal = true
  }

  function cancelReset() {
    if (!resetInProgress) showResetModal = false
  }

  async function confirmReset() {
    if (resetInProgress) return
    resetInProgress = true
    try {
      const fresh = await ResetRules()
      if (Array.isArray(fresh) && fresh.length > 0) {
        rules = fresh.map((r) => {
          const rr = r
          const anyr = /** @type {any} */ (rr)
          const rawSeverity = (rr?.severity ?? anyr?.Severity ?? '').toString().trim()
          return {
            id: rr?.id ?? anyr?.ID,
            name: rr?.name ?? anyr?.Name,
            pattern: rr?.pattern ?? anyr?.Pattern,
            isEnabled: rr?.isEnabled ?? anyr?.IsEnabled ?? true,
            isLiteral: rr?.isLiteral ?? anyr?.IsLiteral ?? true,
            category: ((rr?.category ?? anyr?.Category ?? '').toString().trim() || 'Custom'),
            severity: normSeverity(rawSeverity || 'Medium'),
            description: rr?.description ?? anyr?.Description,
            replacement: rr?.replacement ?? anyr?.Replacement,
          }
        })
      } else {
        await refreshRules()
      }
      showResetModal = false
      await redactNow(rawInput)
    } catch (e) {
      rulesError = e?.message ?? 'Failed to restore defaults'
    } finally {
      resetInProgress = false
    }
  }

  function handleGlobalKeydown(e) {
    if (e.key === 'Escape') {
      if (searchOpen) {
        searchOpen = false
        searchQuery = ''
        if (searchInputEl) searchInputEl.blur()
        e.preventDefault()
        return
      }
      if (showResetModal) cancelReset()
      else if (showWarningModal) cancelRiskToggle()
      else if (showSettingsModal) closeSettingsModal()
      else if (showAboutModal) closeAboutModal()
    }
    if ((e.ctrlKey || e.metaKey) && e.key?.toLowerCase() === 'f') {
      e.preventDefault()
      searchOpen = !searchOpen
      if (searchOpen) {
        searchQuery = ''
        tick().then(() => searchInputEl?.focus())
      }
    }
    if (e.ctrlKey && e.altKey && e.key?.toLowerCase() === 'c') {
      e.preventDefault()
      clearAll()
    }
    if ((e.ctrlKey || e.metaKey) && (e.key === '=' || e.key === '+')) {
      e.preventDefault()
      adjustZoom(ZOOM_STEP)
    }
    if ((e.ctrlKey || e.metaKey) && e.key === '-') {
      e.preventDefault()
      adjustZoom(-ZOOM_STEP)
    }
  }

  function toggleSearch() {
    searchOpen = !searchOpen
    if (searchOpen) {
      searchQuery = ''
      tick().then(() => searchInputEl?.focus())
    }
  }

  function escapeRegex(s) {
    return s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
  }

  function getHighlightSegments(text, query) {
    if (!text) return []
    const q = (query ?? '').trim()
    if (!q) return [{ type: 'text', value: text }]
    const regex = new RegExp(escapeRegex(q), 'gi')
    const segments = []
    let lastIndex = 0
    let m
    while ((m = regex.exec(text)) !== null) {
      if (m.index > lastIndex) segments.push({ type: 'text', value: text.slice(lastIndex, m.index) })
      segments.push({ type: 'match', value: text.slice(m.index, m.index + m[0].length) })
      lastIndex = m.index + m[0].length
    }
    if (lastIndex < text.length) segments.push({ type: 'text', value: text.slice(lastIndex) })
    return segments
  }

  function syncRawHighlightScroll() {
    if (rawInputEl && rawHighlightEl) {
      rawHighlightEl.scrollTop = rawInputEl.scrollTop
      rawHighlightEl.scrollLeft = rawInputEl.scrollLeft
    }
  }

  function syncSafeHighlightScroll() {
    if (sanitizedOutputEl && safeHighlightEl) {
      safeHighlightEl.scrollTop = sanitizedOutputEl.scrollTop
      safeHighlightEl.scrollLeft = sanitizedOutputEl.scrollLeft
    }
  }
</script>

<svelte:window on:keydown={handleGlobalKeydown} />

<div class="app-zoom-wrapper">
<div class="app" class:app--satellite={isSatellite} style="zoom: {zoomLevel / 100}">
  {#if appInitialized}
  <!-- Global search: hidden until Ctrl+F or search icon; overlay in Satellite to avoid layout shift -->
  <div
    class="global-search"
    class:global-search--open={searchOpen}
    class:global-search--overlay={isSatellite}
    role="search"
    aria-label="Search in Raw and Sanitized panels"
  >
    <div class="global-search__bar">
      <svg class="global-search__icon" xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
        <circle cx="11" cy="11" r="8" />
        <path d="m21 21-4.35-4.35" />
      </svg>
      <input
        bind:this={searchInputEl}
        type="search"
        class="global-search__input"
        bind:value={searchQuery}
        placeholder="Search in Raw and Sanitized…"
        aria-label="Search text"
        autocomplete="off"
        on:keydown={(e) => e.key === 'Escape' && (searchOpen = false) && searchInputEl?.blur()}
      />
      {#if searchQuery}
        <span class="global-search__hint">Live highlight: Raw = yellow, Redacted = green</span>
      {/if}
    </div>
  </div>

  <header class="header" class:header--ghost-active={ghostModeEnabled} style="background-color: #161b22; --wails-draggable: drag;">
    <div class="header__left">
      <button
        type="button"
        class="header__satellite"
        class:header__satellite--active={isSatellite}
        on:click={toggleSatellite}
        title={isSatellite ? 'Exit Satellite Mode (slim sidebar)' : 'Satellite Mode: slim always-on-top sidebar'}
        aria-label={isSatellite ? 'Exit Satellite Mode' : 'Enter Satellite Mode'}
        aria-pressed={isSatellite}
      >
        <svg class="header__satellite-icon" xmlns="http://www.w3.org/2000/svg" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <path d="M12 2v6l5 5 4-4v12H3l4-4-5-5v-6z" />
        </svg>
        <span class="header__satellite-label">Satellite</span>
      </button>
      {#if isSatellite}
        <button
          type="button"
          class="header__view-toggle"
          class:header__view-toggle--off={!satelliteShowRawInput}
          on:click={() => (satelliteShowRawInput = !satelliteShowRawInput)}
          title={satelliteShowRawInput ? 'Hide Raw Input' : 'Show Raw Input'}
          aria-label={satelliteShowRawInput ? 'Hide Raw Input' : 'Show Raw Input'}
        >
          <svg class="header__view-icon" xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
            <path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z" />
            <circle cx="12" cy="12" r="3" />
          </svg>
          <span class="header__view-label">View</span>
        </button>
      {/if}
      <div class="brand">
        <img src={brandLogo} alt="OneBoard Safe Paste" class="brand__logo" />
      </div>
    </div>
    <div class="tabs" role="tablist" aria-label="Main navigation">
      <button
        type="button"
        class="tab"
        class:tab--active={activeTab === 'Workspace'}
        on:click={() => (activeTab = 'Workspace')}
      >
        Workspace
      </button>
      <button
        type="button"
        class="tab"
        class:tab--active={activeTab === 'Rules Library'}
        on:click={() => (activeTab = 'Rules Library')}
      >
        Rules Library
      </button>
    </div>
    <div class="header__right">
      <button
        type="button"
        class="header__search-btn"
        class:header__search-btn--active={searchOpen}
        title="Search in Raw and Sanitized (Ctrl+F)"
        aria-label="Search"
        aria-pressed={searchOpen}
        on:click={toggleSearch}
      >
        <svg class="header__search-icon" xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
          <circle cx="11" cy="11" r="8" />
          <path d="m21 21-4.35-4.35" />
        </svg>
      </button>
      <button
        type="button"
        class="header__ghost"
        class:header__ghost--active={ghostModeEnabled}
        on:click={() => {
          ghostModeEnabled = !ghostModeEnabled
          persistGhostMode()
        }}
        title={ghostModeEnabled ? 'Ghost Mode: Clipboard Firewall active — background monitoring and real-time redaction' : 'Enable Ghost Mode to activate the Clipboard Firewall. This provides background monitoring and real-time redaction of sensitive data.'}
        aria-label={ghostModeEnabled ? 'Exit Ghost Mode' : 'Enter Ghost Mode'}
        aria-pressed={ghostModeEnabled}
      >
        <svg class="header__ghost-icon" xmlns="http://www.w3.org/2000/svg" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <path d="M17 21v-2a4 4 0 0 0-4-4H5a4 4 0 0 0-4 4v2" />
          <circle cx="9" cy="7" r="4" />
          <path d="M23 21v-2a4 4 0 0 0-3-3.87" />
          <path d="M16 3.13a4 4 0 0 1 0 7.75" />
        </svg>
        <span class="header__ghost-label">Ghost</span>
      </button>
      <label
        class="debug-toggle"
        title="Show which rule applied to each redaction"
      >
        <input type="checkbox" bind:checked={debugMode} on:change={onDebugModeChange} />
        <span class="debug-toggle__label">Debug</span>
      </label>
      <button
        type="button"
        class="header__about-btn"
        title="About OneBoard Safe Paste"
        aria-label="About"
        on:click={openAboutModal}
      >
        <svg class="header__about-icon" xmlns="http://www.w3.org/2000/svg" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <circle cx="12" cy="12" r="10" />
          <path d="M12 16v-4" />
          <path d="M12 8h.01" />
        </svg>
      </button>
      <button
        type="button"
        class="header__settings-btn"
        title="Settings"
        aria-label="Settings"
        on:click={openSettingsModal}
      >
        <svg class="header__settings-icon" xmlns="http://www.w3.org/2000/svg" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <path d="M12 1v2" />
          <path d="M12 21v2" />
          <path d="M4.22 4.22l1.42 1.42" />
          <path d="M18.36 18.36l1.42 1.42" />
          <path d="M1 12h2" />
          <path d="M21 12h2" />
          <path d="M4.22 19.78l1.42-1.42" />
          <path d="M18.36 5.64l1.42-1.42" />
          <circle cx="12" cy="12" r="3" />
        </svg>
      </button>
      <div class="window-controls">
        <button type="button" class="window-control window-control--minimize" aria-label="Minimize" on:click={() => WindowMinimise()}>
          <svg width="10" height="1" viewBox="0 0 10 1" fill="currentColor" aria-hidden="true"><rect width="10" height="1" /></svg>
        </button>
        <button type="button" class="window-control window-control--maximize" aria-label="Maximize" on:click={() => WindowToggleMaximise()}>
          <svg width="10" height="10" viewBox="0 0 10 10" fill="none" stroke="currentColor" stroke-width="1.25" aria-hidden="true"><rect x="0.5" y="0.5" width="9" height="9" rx="0.5" /></svg>
        </button>
        <button type="button" class="window-control window-control--close" aria-label="Close" on:click={handleWindowClose}>
          <svg width="10" height="10" viewBox="0 0 10 10" fill="none" stroke="currentColor" stroke-width="1.25" stroke-linecap="round" aria-hidden="true"><path d="M1 1l8 8M9 1L1 9" /></svg>
        </button>
      </div>
    </div>
  </header>

  {#if debugMode && showDebugWarning}
    <div class="debug-warning" role="alert">
      <span class="debug-warning__text">
        Debug mode shows original secret values on screen. Turn off before screen sharing or recordings.
      </span>
      <button type="button" class="debug-warning__dismiss" on:click={dismissDebugWarning}>Dismiss</button>
    </div>
  {/if}

  {#if activeTab === 'Workspace'}
  <main
    class="workspace"
    class:workspace--with-debug={debugMode}
    class:workspace--full={!debugMode}
    class:workspace--satellite={isSatellite}
    class:satellite-layout={isSatellite}
    bind:this={dropZoneEl}
    on:dragover={handleDragOver}
    on:dragenter={handleDragEnter}
    on:dragleave={handleDragLeave}
    on:drop={handleDrop}
  >
    {#if isDraggingOver}
      <div class="drop-overlay" aria-live="polite">Drop files here</div>
    {/if}
    {#if dropToastMessage}
      <div class="drop-toast" role="alert">{dropToastMessage}</div>
    {/if}
      <section class="panel panel--raw" class:panel--raw-hidden={isSatellite && !satelliteShowRawInput}>
      <div class="panel__top">
        <div class="panel__title">Raw Input</div>
        <div class="panel__actions">
          <button
            class="btn btn--icon"
            type="button"
            on:click={clearAll}
            disabled={!rawInput && !sanitizedOutput}
            title="Clear all (Ctrl+Alt+C)"
            aria-label="Clear all"
          >
            <svg class="btn__icon" xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
              <polyline points="3 6 5 6 21 6" />
              <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2" />
              <line x1="10" y1="11" x2="10" y2="17" />
              <line x1="14" y1="11" x2="14" y2="17" />
            </svg>
            <span class="btn__icon-label">Clear</span>
          </button>
        </div>
      </div>
      <div class="panel__textarea-wrap">
        {#if searchQuery.trim()}
          <div
            bind:this={rawHighlightEl}
            class="highlight-mirror highlight-mirror--raw"
            aria-hidden="true"
          >
            <div class="highlight-mirror__content">
              {#each getHighlightSegments(rawInput, searchQuery) as seg}
                {#if seg.type === 'match'}
                  <mark class="highlight highlight--raw">{seg.value}</mark>
                {:else}
                  {seg.value}
                {/if}
              {/each}
            </div>
          </div>
        {/if}
        <textarea
          bind:this={rawInputEl}
          class="textarea"
          class:textarea--search-active={searchQuery.trim()}
          bind:value={rawInput}
          placeholder={isDraggingOver ? 'Release to Redact...' : 'Paste logs or drag & drop one or more files here...'}
          spellcheck="false"
          autocomplete="off"
          autocapitalize="off"
          autocorrect="off"
          on:scroll={syncRawHighlightScroll}
        />
      </div>
    </section>

    <section class="panel panel--safe" class:panel--autocopied-flash={autoCopiedFlash}>
      <div class="panel__top">
        <div class="panel__title">Sanitized Output</div>
        <div class="panel__actions">
          <button
            class="btn btn--icon"
            type="button"
            on:click={downloadOutput}
            disabled={!sanitizedOutput}
            title="Download combined output"
            aria-label="Download"
          >
            <svg class="btn__icon" xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
              <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" />
              <polyline points="7 10 12 15 17 10" />
              <line x1="12" y1="15" x2="12" y2="3" />
            </svg>
            <span class="btn__icon-label">Download</span>
          </button>
          <button
            class="btn btn--icon"
            type="button"
            on:click={clearAll}
            disabled={!rawInput && !sanitizedOutput}
            title="Clear all (Ctrl+Alt+C)"
            aria-label="Clear"
          >
            <svg class="btn__icon" xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
              <polyline points="3 6 5 6 21 6" />
              <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2" />
              <line x1="10" y1="11" x2="10" y2="17" />
              <line x1="14" y1="11" x2="14" y2="17" />
            </svg>
            <span class="btn__icon-label">Clear</span>
          </button>
          <label class="autocopy-toggle">
            <input type="checkbox" bind:checked={isAutoCopyEnabled} on:change={persistAutoCopy} />
            <span class="autocopy-toggle__label">Auto-Copy</span>
          </label>
          <label
            class="autocopy-toggle"
            title="Keep only lines with Error, Exception, Fail, Critical, 404, 500 plus 5 lines above/below to stay under token limits"
          >
            <input type="checkbox" bind:checked={smartTrimEnabled} />
            <span class="autocopy-toggle__label">Smart Trim</span>
          </label>
          <!-- Auto Paste: UI removed (was destructive); logic kept below for potential re-use -->
          <button class="btn" type="button" on:click={copyToClipboard} disabled={!sanitizedOutput}>
            {copyCheckFlash ? '✓' : (copyStatus || 'Copy')}
          </button>
          {#if autoCopiedFlash}
            <span class="autocopied-toast" aria-live="polite">Auto-Copied!</span>
          {/if}
        </div>
      </div>
      <div class="panel__textarea-wrap">
        {#if searchQuery.trim()}
          <div
            bind:this={safeHighlightEl}
            class="highlight-mirror highlight-mirror--safe"
            aria-hidden="true"
          >
            <div class="highlight-mirror__content">
              {#each getHighlightSegments(displayedSanitizedOutput, searchQuery) as seg}
                {#if seg.type === 'match'}
                  <mark class="highlight highlight--safe">{seg.value}</mark>
                {:else}
                  {seg.value}
                {/if}
              {/each}
            </div>
          </div>
        {/if}
        {#if debugMode && outputSegments && outputSegments.length > 0}
          <div bind:this={sanitizedOutputEl} class="textarea textarea--readonly textarea--safe output-debug" class:textarea--search-active={searchQuery.trim()} role="textbox" aria-readonly="true" on:scroll={syncSafeHighlightScroll}>
            {#each outputSegments as seg}
              {#if seg.type === 'text'}
                {seg.value}
              {:else}
                <span class="output-redaction" title="Applied: {seg.ruleName}">{seg.value}</span>
              {/if}
            {/each}
          </div>
        {:else}
          <textarea bind:this={sanitizedOutputEl} class="textarea textarea--readonly textarea--safe" class:textarea--search-active={searchQuery.trim()} readonly value={displayedSanitizedOutput} spellcheck="false" on:scroll={syncSafeHighlightScroll} />
        {/if}
      </div>
    </section>

    {#if debugMode}
      <aside class="debug-console" aria-label="Debug Console">
        <div class="debug-console__title">Debug Console</div>
        <div class="debug-console__subtitle">Rules applied to current input</div>
        {#if redactionEvents.length === 0}
          <p class="debug-console__empty">No redactions yet. Paste text to see which rules fire.</p>
        {:else}
          <ul class="debug-console__list">
            {#each redactionEvents as ev, i}
              <li class="debug-console__item" title="{ev.originalText?.slice(0, 80)}{ev.originalText?.length > 80 ? '…' : ''}">
                <span class="debug-console__rule">{ev.ruleName}</span>
                <span class="debug-console__repl">{ev.replacement}</span>
              </li>
            {/each}
          </ul>
        {/if}
      </aside>
    {/if}
  </main>
  {:else}
  <main class="library" role="region" aria-label="Rules Library">
    <div class="library__bar">
      <div class="library__search">
        <label for="rule-search" class="library__label">Search</label>
        <div class="search-wrap">
          <input id="rule-search" class="input search-wrap__input" bind:value={ruleSearch} placeholder="Search by name or description…" />
          <button
            type="button"
            class="search-wrap__clear"
            on:click={() => (ruleSearch = '')}
            disabled={!ruleSearch?.trim()}
            title="Clear search"
            aria-label="Clear search"
          >
            <svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
              <line x1="18" y1="6" x2="6" y2="18" />
              <line x1="6" y1="6" x2="18" y2="18" />
            </svg>
          </button>
        </div>
      </div>
      <div class="chips" role="tablist" aria-label="Category filter">
        {#each ['All', 'Cloud & Auth', 'Network', 'Sensitive Data', 'System', 'Custom'] as chip}
          <button
            type="button"
            class="chip"
            class:chip--active={ruleCategoryFilter === chip}
            on:click={() => (ruleCategoryFilter = chip)}
          >
            {chip}
          </button>
        {/each}
      </div>
    </div>
    {#if ruleCategoryFilter === 'Custom'}
    <div class="library__quickadd">
      <div class="library__sectionTitle">Custom Rules</div>
      <div class="quickadd--row">
        <button
          class="btn"
          type="button"
          on:click={openAddRuleModal}
        >
          New Rule
        </button>
        {#if quickAddStatus}
          <span class="muted">{quickAddStatus}</span>
        {/if}
      </div>
    </div>
    <div
      class="library__bulkadd"
      class:library__bulkadd--dragging={isBulkDragging}
      bind:this={bulkDropZoneEl}
      on:dragover={handleBulkDragOver}
      on:dragenter={handleBulkDragEnter}
      on:dragleave={handleBulkDragLeave}
      on:drop={handleBulkDrop}
    >
      <div class="library__sectionTitle">Bulk add: mask hostnames, usernames, or IPs</div>
      <p class="library__hint">Paste a list (one per line or comma-separated), or drop a .txt, .log, or .csv file here.</p>
      {#if isBulkDragging}
        <div class="bulkadd__dropoverlay">Drop file to load list…</div>
      {/if}
      <textarea
        class="input input--bulk"
        bind:value={bulkListText}
        placeholder="e.g.&#10;server1.prod.example.com&#10;db-host-02&#10;admin_user, deploy_bot"
        rows="4"
        spellcheck="false"
      />
      <div class="bulkadd__row">
        <label for="bulk-mask-type" class="library__label">Mask as</label>
        <select id="bulk-mask-type" class="input input--select" bind:value={bulkMaskType}>
          <option value="host">Hostnames → [HOST]</option>
          <option value="user">Usernames → [USER]</option>
          <option value="ip">IPs → [IP]</option>
          <option value="custom">Generic → [REDACTED]</option>
        </select>
        <label for="bulk-category" class="library__label">Category</label>
        <select id="bulk-category" class="input input--select" bind:value={bulkCategory}>
          <option value="System">System</option>
          <option value="Cloud & Auth">Cloud & Auth</option>
          <option value="Network">Network</option>
          <option value="Sensitive Data">Sensitive Data</option>
        </select>
        <label for="bulk-severity" class="library__label">Severity</label>
        <select id="bulk-severity" class="input input--select" bind:value={bulkSeverity}>
          <option value="Critical">Critical</option>
          <option value="High">High</option>
          <option value="Medium">Medium</option>
        </select>
        <button
          class="btn"
          type="button"
          on:click={addBulkRules}
          disabled={bulkInProgress || !bulkListText?.trim()}
        >
          {bulkInProgress ? 'Adding…' : 'Add bulk rule'}
        </button>
      </div>
      {#if bulkStatus}
        <p class="muted library__status">{bulkStatus}</p>
      {/if}
    </div>
    {/if}
    <div class="library__table-wrap">
      {#if rulesLoading}
        <div class="muted">Loading rules…</div>
      {:else if rulesError}
        <div class="error">{rulesError}</div>
      {:else if filteredRules.length === 0}
        <div class="muted">No rules found.</div>
      {:else}
        <table class="library__table">
          <colgroup>
            <col class="library__col library__col--enabled" />
            <col class="library__col library__col--category" />
            <col class="library__col library__col--name" />
            <col class="library__col library__col--severity" />
            <col class="library__col library__col--pattern" />
            <col class="library__col library__col--replacement" />
            <col class="library__col library__col--actions" />
          </colgroup>
          <thead>
            <tr>
              <th class="library__th library__th--enabled">Enabled</th>
              <th class="library__th library__th--category">Category</th>
              <th class="library__th library__th--name">Name</th>
              <th class="library__th library__th--severity">Severity</th>
              <th class="library__th library__th--pattern">Pattern</th>
              <th class="library__th library__th--replacement">Replacement</th>
              <th class="library__th library__th--actions">Actions</th>
            </tr>
          </thead>
          <tbody>
            {#each groupedFilteredRules as group (group.category)}
              <tr class="library__group">
                <td class="library__cell library__cell--group" colspan="7">{group.category}</td>
              </tr>
              {#each group.rules as rule (rule.id)}
                <tr class="library__row">
                <td class="library__cell library__cell--enabled library__cell--center">
                  <label class="toggle">
                    <input
                      type="checkbox"
                      checked={rule.isEnabled}
                      on:click={(e) => {
                        e.preventDefault()
                        requestToggleRule(rule.id, !rule.isEnabled)
                      }}
                    />
                    <span class="toggle__track" aria-hidden="true" />
                  </label>
                </td>
                <td class="library__cell library__cell--category">{rule.category || 'Custom'}</td>
                <td
                  class="library__cell library__cell--name"
                  title={`${rule?.name ?? ''}${rule?.description ? `\n${rule.description}` : ''}`}
                >
                  <span class="ruleName">
                    {rule.name}
                    {#if (rule?.source ?? '').toString().toLowerCase() === 'custom'}
                      <span class="badge badge--custom" title="Custom rule">Custom</span>
                    {/if}
                  </span>
                </td>
                <td class="library__cell library__cell--severity">
                  <span
                    class="badge badge--{rule.severity?.toLowerCase() ?? 'medium'}"
                    title="Severity: {rule.severity ?? 'Medium'}"
                  >
                    {rule.severity ?? 'Medium'}
                  </span>
                </td>
                <td class="library__cell library__cell--pattern" title={rule.pattern ?? ''}>
                  {(rule.pattern ?? '').slice(0, 48)}{(rule.pattern ?? '').length > 48 ? '…' : ''}
                </td>
                <td class="library__cell library__cell--replacement">
                  <input
                    class="input input--compact input--cell"
                    value={rule.replacement ?? ''}
                    placeholder="[SECRET]"
                    on:input={(e) => updateRule(rule.id, { replacement: e.currentTarget.value })}
                  />
                </td>
                <td class="library__cell library__cell--actions">
                  {#if (rule?.source ?? '').toString().toLowerCase() === 'custom'}
                    <button class="iconbtn iconbtn--danger" type="button" on:click={() => deleteCustomRule(rule.id)}>
                      Delete
                    </button>
                  {:else}
                    <span class="muted">—</span>
                  {/if}
                </td>
                </tr>
              {/each}
            {/each}
          </tbody>
        </table>
      {/if}
    </div>
    <div class="library__footer">
      <button type="button" class="btn btn--ghost btn--secondary" on:click={openResetModal}>
        Restore Default Settings
      </button>
    </div>
  </main>
  {/if}

  <footer class="footer">
    <div class="footer__left">
      {#if !isSatellite}
        Redaction on-device. No content uploaded for processing.
      {/if}
      <label
        class="footer__toggle"
        title="Clear clipboard 30 seconds after Copy so redacted text is not left on the clipboard"
      >
        <input type="checkbox" bind:checked={timedClipboardWipeEnabled} on:change={persistTimedClipboardWipe} />
        <span class="footer__toggleLabel">Timed Clipboard Wipe (30s)</span>
      </label>
    </div>
    <div class="footer__right">
      <div class="footer__prompt-wrap">
        <label for="footer-ai-prompt" class="footer__prompt-label">AI Prompt</label>
        <select id="footer-ai-prompt" class="footer__prompt-select" bind:value={selectedAiPromptTemplate} title="Add this prompt to the beginning of the sanitized output">
          {#each AI_PROMPT_TEMPLATES as t}
            <option value={t.id}>{t.label}</option>
          {/each}
        </select>
      </div>
      <div class="footer__stats">
        <span class="footer__stat" title="Enabled rules">
          <span class="footer__statIcon footer__statIcon--shield" aria-hidden="true">🛡</span>
          {Array.isArray(rules) ? rules.length : 0} Rules
        </span>
        <span class="footer__statDivider">|</span>
        <span class="footer__stat footer__stat--custom" class:footer__stat--customActive={(Array.isArray(rules) ? rules.filter((r) => r && (r?.source ?? '').toString().toLowerCase() === 'custom') : []).length > 0} title="Custom rules">{Array.isArray(rules) ? rules.filter((r) => r && (r?.source ?? '').toString().toLowerCase() === 'custom').length : 0} Custom</span>
      </div>
      <span class="status" title="Local-first redaction — all processing on this device">
        <span class="status__dot" aria-hidden="true" />
        <span class="status__text">Silo Mode: Active</span>
      </span>
    </div>
  </footer>

  {#if showWarningModal}
    <button
      class="modalOverlay"
      type="button"
      aria-label="Close warning"
      on:click={(e) => e.target === e.currentTarget && cancelRiskToggle()}
    >
      <div class="modal" role="dialog" aria-modal="true">
        <div class="modal__title">Confirm Risk</div>
        <div class="modal__body">
          Warning: You are disabling a Security-Critical rule. This may result in leaking production credentials to
          third-party AI models. Are you sure?
        </div>
        <div class="modal__actions">
          <button class="btn btn--ghost" type="button" on:click={cancelRiskToggle}>Cancel</button>
          <button class="btn btn--danger" type="button" on:click={confirmRiskAndDisable}>Confirm Risk</button>
        </div>
      </div>
    </button>
  {/if}

  {#if showAddRuleModal}
    <div class="modalOverlay" role="presentation" on:click={closeAddRuleModal} on:keydown|stopPropagation={() => {}}>
      <div class="modal" role="dialog" aria-modal="true" aria-label="Add custom rule" on:click|stopPropagation on:keydown|stopPropagation={() => {}}>
        <div class="modal__title">Add Rule</div>
        <div class="modal__body">
          <div class="quickadd quickadd--modal">
            <label class="library__label" for="add-rule-pattern">Pattern</label>
            <input id="add-rule-pattern" class="input" bind:value={quickAddText} placeholder="Enter literal string or regex pattern..." />
            <label class="library__label" for="add-rule-replacement">Replacement</label>
            <input id="add-rule-replacement" class="input" bind:value={quickAddReplacement} placeholder="[SECRET] or leave blank" />
            <label class="library__label" for="add-rule-category">Category</label>
            <select id="add-rule-category" class="input input--select" bind:value={quickAddCategory}>
              <option value="Cloud & Auth">Cloud & Auth</option>
              <option value="Network">Network</option>
              <option value="Sensitive Data">Sensitive Data</option>
              <option value="System">System</option>
            </select>
            <label class="library__label" for="add-rule-severity">Severity</label>
            <select id="add-rule-severity" class="input input--select" bind:value={quickAddSeverity}>
              <option value="Critical">Critical</option>
              <option value="High">High</option>
              <option value="Medium">Medium</option>
            </select>
          </div>
        </div>
        <div class="modal__actions">
          <button class="btn btn--ghost" type="button" on:click={closeAddRuleModal}>Cancel</button>
          <button class="btn" type="button" on:click={addQuickCustom} disabled={!quickAddText.trim()}>Add</button>
        </div>
      </div>
    </div>
  {/if}

  {#if showResetModal}
    <button
      class="modalOverlay"
      type="button"
      aria-label="Close"
      on:click={(e) => e.target === e.currentTarget && cancelReset()}
    >
      <div class="modal" role="dialog" aria-modal="true">
        <div class="modal__title">Restore Default Settings</div>
        <div class="modal__body">
          Are you sure? This will delete all custom rules (like Billy Bob) and revert to factory settings.
        </div>
        <div class="modal__actions">
          <button class="btn btn--ghost" type="button" on:click={cancelReset} disabled={resetInProgress}>Cancel</button>
          <button class="btn btn--danger" type="button" on:click={confirmReset} disabled={resetInProgress}>
            {resetInProgress ? 'Restoring…' : 'Restore Defaults'}
          </button>
        </div>
      </div>
    </button>
  {/if}

  {#if showAboutModal}
    <button
      class="modalOverlay modalOverlay--about"
      type="button"
      aria-label="Close"
      on:click={(e) => e.target === e.currentTarget && closeAboutModal()}
    >
      <div class="modal modal--about" role="dialog" aria-modal="true" aria-label="About OneBoard Safe Paste">
        <div class="modal__title modal__title--about">About OneBoard Safe Paste</div>
        <div class="modal__body modal__body--about modal__body--about-scroll">
          <About appVersion={APP_VERSION} />
        </div>
        <div class="modal__actions modal__actions--about">
          <button class="btn btn--about-close" type="button" on:click={closeAboutModal}>Close</button>
        </div>
      </div>
    </button>
  {/if}

  {#if showSettingsModal}
    <button
      class="modalOverlay"
      type="button"
      aria-label="Close"
      on:click={(e) => e.target === e.currentTarget && closeSettingsModal()}
    >
      <div class="modal modal--settings" role="dialog" aria-modal="true" aria-label="Settings">
        <div class="modal__title">Settings</div>
        <div class="modal__body">
          <p class="settings__lead">
            OneBoard Safe Paste is open source. All features run locally on your device — no license or activation required.
          </p>
          <p class="settings__meta">
            Version <span class="settings__version">{APP_VERSION}</span>
          </p>
          <a
            class="settings__link"
            href="https://github.com/phat7000/OneBoardSafePaste"
            target="_blank"
            rel="noopener noreferrer"
          >
            View source on GitHub
          </a>
        </div>
        <div class="modal__actions">
          <button type="button" class="btn btn--ghost btn--secondary" on:click={closeSettingsModal}>Close</button>
        </div>
      </div>
    </button>
  {/if}

  {#if showZoomGhost}
    <div class="zoom-ghost" class:zoom-ghost--fade-out={zoomGhostFadeOut} aria-live="polite">{zoomLevel}%</div>
  {/if}
  {#if ghostToast}
    <div class="ghost-toast" role="status" aria-live="polite">Ghost Redaction Applied</div>
  {/if}
  {#if clipboardWipeSecondsLeft > 0}
    <div class="clipboard-wipe-toast" role="status" aria-live="polite">
      Clipboard will clear in {clipboardWipeSecondsLeft}s
    </div>
  {/if}
  {:else}
  <div class="app-loading" role="status" aria-live="polite">Loading rules…</div>
  {/if}
</div>
</div>

<style>
  .app-zoom-wrapper {
    position: relative;
    min-height: 100vh;
    width: 100%;
    background: #0d1117;
  }

  .zoom-ghost {
    position: fixed;
    left: 50%;
    bottom: 24%;
    transform: translateX(-50%);
    font-size: 2.5rem;
    font-weight: 600;
    color: rgba(255, 255, 255, 0.4);
    pointer-events: none;
    z-index: 9999;
    user-select: none;
    animation: zoom-ghost-in 0.25s ease-out forwards;
    transition: opacity 0.5s ease-out;
  }

  .zoom-ghost--fade-out {
    opacity: 0;
  }

  @keyframes zoom-ghost-in {
    0% {
      opacity: 0;
      transform: translateX(-50%) scale(0.92);
    }
    100% {
      opacity: 1;
      transform: translateX(-50%) scale(1);
    }
  }

  :global(:root) {
    color-scheme: dark;
  }

  :global(*),
  :global(*::before),
  :global(*::after) {
    box-sizing: border-box;
  }

  .app-loading {
    display: flex;
    align-items: center;
    justify-content: center;
    min-height: 100vh;
    color: rgba(255, 255, 255, 0.6);
    font-size: 1rem;
  }

  .app {
    min-height: 100vh;
    width: 100%;
    display: grid;
    grid-template-rows: auto auto 1fr auto;
    grid-template-columns: 1fr;
    background: #0d1117;
    color: rgba(255, 255, 255, 0.92);
    font-family: "Inter", sans-serif;
    overflow: hidden;
    transition: opacity 0.2s ease-out;
    transform-origin: 0 0;
  }

  .global-search {
    grid-column: 1 / -1;
    max-height: 0;
    overflow: hidden;
    transition: max-height 0.2s ease-out;
    background: #161b22;
    border-bottom: 1px solid #30363d;
  }

  .global-search--open {
    max-height: 52px;
  }

  .global-search--overlay.global-search--open {
    position: fixed;
    top: 0;
    left: 0;
    right: 0;
    z-index: 200;
    max-height: none;
    height: 48px;
    box-shadow: 0 4px 12px rgba(0, 0, 0, 0.35);
  }

  .global-search__bar {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 10px 16px;
    min-height: 48px;
  }

  .global-search__icon {
    flex-shrink: 0;
    color: rgba(255, 255, 255, 0.5);
  }

  .global-search__input {
    flex: 1;
    min-width: 0;
    padding: 8px 12px;
    border: 1px solid #30363d;
    border-radius: 8px;
    background: #0d1117;
    color: rgba(255, 255, 255, 0.95);
    font-size: 13px;
    font-family: inherit;
    outline: none;
    transition: border-color 0.15s ease;
  }

  .global-search__input::placeholder {
    color: rgba(255, 255, 255, 0.4);
  }

  .global-search__input:focus {
    border-color: rgba(88, 166, 255, 0.6);
  }

  .global-search__hint {
    flex-shrink: 0;
    font-size: 11px;
    color: rgba(255, 255, 255, 0.5);
    white-space: nowrap;
  }

  @media (max-width: 640px) {
    .global-search__hint {
      display: none;
    }
  }

  .header__search-btn {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 36px;
    height: 36px;
    padding: 0;
    border: 1px solid #30363d;
    border-radius: 8px;
    background: #21262d;
    color: rgba(255, 255, 255, 0.75);
    cursor: pointer;
    transition: background 0.15s ease, border-color 0.15s ease, color 0.15s ease;
  }

  .header__search-btn:hover {
    background: #30363d;
    color: rgba(255, 255, 255, 0.95);
    border-color: #8b949e;
  }

  .header__search-btn--active {
    background: rgba(88, 166, 255, 0.2);
    border-color: rgba(88, 166, 255, 0.5);
    color: rgba(255, 255, 255, 0.95);
  }

  .header__search-icon {
    flex-shrink: 0;
  }

  .app--satellite {
    opacity: 1;
  }

  /* Satellite: single compact header row – logo, search, Debug, About on same line */
  .app--satellite .header {
    grid-template-columns: 1fr auto 1fr;
    gap: 6px 8px;
    padding: 8px 10px;
  }

  .app--satellite .header .brand__logo {
    height: 22px;
    max-width: 100px;
  }

  .app--satellite .header__right {
    gap: 6px;
  }

  .app--satellite .header .header__ghost {
    padding: 5px 8px;
    font-size: 11px;
  }

  .app--satellite .header .debug-toggle {
    font-size: 11px;
  }

  .app--satellite .header .pill {
    font-size: 10px;
    padding: 4px 6px;
  }

  .app--satellite .header__search-btn {
    width: 28px;
    height: 28px;
    padding: 0;
  }

  .app--satellite .header__search-icon {
    width: 14px;
    height: 14px;
  }

  .app--satellite .header .tab {
    padding: 5px 8px;
    font-size: 11px;
  }

  /* Satellite: reduce padding and font sizes for data-to-screen ratio */
  .app--satellite .workspace {
    padding: 8px;
    gap: 8px;
  }

  .app--satellite .panel {
    border-radius: 10px;
  }

  .app--satellite .panel__top {
    padding: 8px 10px 6px;
  }

  .app--satellite .panel__title {
    font-size: 10px;
    letter-spacing: 0.2px;
  }

  .app--satellite .textarea {
    padding: 8px;
    font-size: 11px;
    line-height: 1.45;
  }

  .app--satellite .panel__actions .btn,
  .app--satellite .autocopy-toggle {
    padding: 4px 8px;
    font-size: 11px;
  }

  .app--satellite .btn__icon-label,
  .app--satellite .autocopy-toggle__label {
    font-size: 11px;
  }

  .app--satellite .drop-overlay {
    inset: 8px;
    font-size: 14px;
  }

  .header__satellite {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    padding: 6px 10px;
    margin-right: 4px;
    border: 1px solid #30363d;
    border-radius: 8px;
    background: #21262d;
    color: rgba(255, 255, 255, 0.75);
    font-size: 12px;
    cursor: pointer;
    transition: background 0.2s ease, border-color 0.2s ease, color 0.2s ease, transform 0.2s ease;
  }

  .header__satellite:hover {
    background: #30363d;
    color: rgba(255, 255, 255, 0.95);
    border-color: #8b949e;
  }

  .header__satellite--active {
    border-color: #58a6ff;
    background: rgba(88, 166, 255, 0.15);
    color: #58a6ff;
  }

  .header__satellite--active:hover {
    background: rgba(88, 166, 255, 0.22);
  }

  .header__satellite-icon {
    flex-shrink: 0;
  }

  .header__satellite-label {
    white-space: nowrap;
  }

  .header__view-toggle {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    padding: 5px 8px;
    border: 1px solid #30363d;
    border-radius: 6px;
    background: #21262d;
    color: rgba(255, 255, 255, 0.7);
    font-size: 11px;
    cursor: pointer;
    transition: background 0.2s ease, border-color 0.2s ease, color 0.2s ease;
  }

  .header__view-toggle:hover {
    background: #30363d;
    color: rgba(255, 255, 255, 0.9);
  }

  .header__view-toggle--off {
    opacity: 0.7;
    border-style: dashed;
  }

  .header__view-icon {
    flex-shrink: 0;
  }

  .header__view-label {
    white-space: nowrap;
  }

  .header {
    container-type: inline-size;
    grid-row: 2;
    grid-column: 1 / -1;
    display: grid;
    grid-template-columns: 1fr auto 1fr;
    align-items: center;
    gap: 0.75rem 1rem;
    padding: 12px 20px;
    padding-right: 8px;
    background: #161b22;
    border-bottom: 1px solid #152238;
    backdrop-filter: blur(6px);
    --wails-draggable: drag;
    position: relative;
  }

  .header::before {
    content: '';
    position: absolute;
    top: 0;
    left: 0;
    right: 0;
    height: 2px;
    background: linear-gradient(90deg, transparent, rgba(88, 166, 255, 0.35) 20%, rgba(0, 163, 163, 0.45) 50%, rgba(88, 166, 255, 0.35) 80%, transparent);
    opacity: 0.85;
    pointer-events: none;
  }

  .header::after {
    content: '';
    position: absolute;
    left: 0;
    right: 0;
    bottom: -1px;
    height: 1px;
    background: transparent;
    pointer-events: none;
    transition: background 0.25s ease, box-shadow 0.25s ease;
  }

  .header button,
  .header .tab,
  .header input,
  .header label,
  .header .window-controls {
    --wails-draggable: no-drag;
  }

  .header--ghost-active {
    border-bottom-color: rgba(0, 163, 163, 0.45);
    box-shadow:
      0 4px 28px rgba(0, 163, 163, 0.14),
      0 0 40px rgba(0, 163, 163, 0.08);
  }

  .header--ghost-active::before {
    background: linear-gradient(90deg, transparent, rgba(0, 163, 163, 0.25) 15%, rgba(0, 229, 229, 0.65) 50%, rgba(0, 163, 163, 0.25) 85%, transparent);
    opacity: 1;
  }

  .header--ghost-active::after {
    background: linear-gradient(90deg, transparent, rgba(0, 163, 163, 0.35), rgba(0, 229, 229, 0.55), rgba(0, 163, 163, 0.35), transparent);
    box-shadow: 0 0 18px rgba(0, 163, 163, 0.35);
  }

  .header__left {
    display: flex;
    align-items: center;
    gap: 0.5rem 1rem;
    justify-self: start;
    min-width: 0;
    overflow: hidden;
  }

  .header .brand {
    min-width: 0;
    flex: 1 1 auto;
    display: flex;
    flex-direction: row;
    align-items: center;
    justify-content: flex-start;
    gap: 12px;
    overflow: hidden;
  }

  .brand__logo {
    display: block;
    height: 34px;
    width: auto;
    max-width: 280px;
    object-fit: contain;
    object-position: left center;
    filter: drop-shadow(0 2px 4px rgba(0, 0, 0, 0.4));
  }

  .header .tabs {
    justify-self: center;
    flex-shrink: 0;
  }

  .header__right {
    justify-self: end;
  }

  .tabs {
    display: flex;
    gap: 0.25rem;
  }

  .tab {
    appearance: none;
    border: 1px solid transparent;
    background: transparent;
    color: rgba(255, 255, 255, 0.7);
    padding: 8px 14px;
    border-radius: 10px;
    font-size: 13px;
    cursor: pointer;
    transition: background 120ms ease, color 120ms ease;
  }

  .tab:hover {
    color: rgba(255, 255, 255, 0.9);
    background: rgba(255, 255, 255, 0.06);
  }

  .tab--active {
    background: #21262d;
    border-color: #30363d;
    color: rgba(255, 255, 255, 0.95);
  }

  .workspace--full {
    grid-column: 1;
  }

  .workspace--with-debug {
    grid-template-columns: 1fr 1fr 280px;
  }

  .workspace--satellite.workspace--with-debug {
    grid-template-columns: 1fr;
  }

  .workspace--satellite .debug-console {
    display: none;
  }

  .header__right {
    display: flex;
    align-items: center;
    gap: 10px;
    --wails-draggable: no-drag;
  }

  .header__ghost {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    padding: 6px 10px;
    margin-right: 4px;
    border: 1px solid #30363d;
    border-radius: 8px;
    background: #21262d;
    color: rgba(255, 255, 255, 0.75);
    font-size: 12px;
    cursor: pointer;
    transition: background 0.2s ease, border-color 0.2s ease, color 0.2s ease, transform 0.2s ease;
  }

  .header__ghost:hover {
    background: #30363d;
    color: rgba(255, 255, 255, 0.95);
    border-color: #8b949e;
  }

  .header__ghost--active {
    border-color: #00A3A3;
    background: linear-gradient(135deg, rgba(0, 163, 163, 0.28), rgba(0, 128, 128, 0.2));
    color: #00e5e5;
    box-shadow:
      0 0 10px rgba(0, 163, 163, 0.45),
      0 0 22px rgba(0, 163, 163, 0.28),
      0 0 40px rgba(0, 163, 163, 0.12);
    animation: ghost-btn-glow 2.6s ease-in-out infinite;
  }

  .header__ghost--active:hover {
    background: linear-gradient(135deg, rgba(0, 163, 163, 0.35), rgba(0, 128, 128, 0.28));
    box-shadow:
      0 0 14px rgba(0, 163, 163, 0.55),
      0 0 28px rgba(0, 163, 163, 0.35),
      0 0 48px rgba(0, 163, 163, 0.16);
  }

  @keyframes ghost-btn-glow {
    0%,
    100% {
      box-shadow:
        0 0 8px rgba(0, 163, 163, 0.38),
        0 0 18px rgba(0, 163, 163, 0.22),
        0 0 32px rgba(0, 163, 163, 0.1);
    }
    50% {
      box-shadow:
        0 0 12px rgba(0, 163, 163, 0.55),
        0 0 26px rgba(0, 163, 163, 0.34),
        0 0 44px rgba(0, 163, 163, 0.14);
    }
  }

  .header__ghost-icon {
    flex-shrink: 0;
  }

  .header__ghost-label {
    white-space: nowrap;
  }

  .window-controls {
    display: flex;
    align-items: center;
    align-self: center;
    margin-left: 8px;
  }

  .window-control {
    appearance: none;
    border: none;
    width: 46px;
    height: 32px;
    padding: 0;
    display: flex;
    align-items: center;
    justify-content: center;
    background: transparent;
    color: rgba(255, 255, 255, 0.85);
    cursor: pointer;
    transition: background 0.15s ease, color 0.15s ease;
  }

  .window-control:hover {
    background: rgba(255, 255, 255, 0.08);
    color: rgba(255, 255, 255, 1);
  }

  .window-control--close:hover {
    background: #e81123;
    color: #fff;
  }

  .debug-toggle {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    cursor: pointer;
    font-size: 12px;
    color: rgba(255, 255, 255, 0.65);
    user-select: none;
  }

  .debug-toggle input {
    margin: 0;
    cursor: pointer;
    accent-color: #d29922;
  }

  .debug-toggle__label {
    white-space: nowrap;
  }

  .iconbtn {
    appearance: none;
    border: 1px solid #30363d;
    background: #21262d;
    color: rgba(255, 255, 255, 0.80);
    border-radius: 10px;
    padding: 8px 10px;
    font-size: 12px;
    cursor: pointer;
    transition: background 120ms ease, border-color 120ms ease;
  }

  .iconbtn:hover {
    background: #30363d;
    border-color: #8b949e;
  }

  .iconbtn--danger {
    border-color: rgba(239, 68, 68, 0.28);
    background: rgba(239, 68, 68, 0.10);
    color: rgba(255, 210, 210, 0.92);
  }

  .iconbtn--danger:hover {
    background: rgba(239, 68, 68, 0.16);
    border-color: rgba(239, 68, 68, 0.38);
  }

  @container (max-width: 920px) {
    .brand__logo {
      max-width: 200px;
    }
  }

  @container (max-width: 720px) {
    .brand__logo {
      max-width: 140px;
    }
  }

  .pill {
    padding: 7px 10px;
    border-radius: 999px;
    font-size: 12px;
    line-height: 12px;
    border: 1px solid #30363d;
    background: #161b22;
    color: rgba(255, 255, 255, 0.82);
  }

  .sidebar {
    grid-row: 3;
    grid-column: 1;
    min-height: 0;
    border: 1px solid rgba(255, 255, 255, 0.10);
    border-radius: 14px;
    background: rgba(255, 255, 255, 0.04);
    box-shadow:
      0 0 0 1px rgba(0, 0, 0, 0.25) inset,
      0 10px 30px rgba(0, 0, 0, 0.25);
    overflow: hidden;
    margin: 1rem 0 1rem 1rem;
    padding: 0.75rem;
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
    height: calc(100% - 2rem);
  }

  .sidebar__title {
    font-size: 12px;
    letter-spacing: 0.35px;
    text-transform: uppercase;
    color: rgba(255, 255, 255, 0.70);
    margin-bottom: 0.25rem;
  }

  .sidebar__scroll {
    min-height: 0;
    overflow-y: auto;
    padding-right: 0.25rem;
  }

  .sidebar__section {
    border-top: 1px solid rgba(255, 255, 255, 0.08);
    padding-top: 0.625rem;
    margin-top: 0.625rem;
  }

  .sidebar__section--soft {
    border-top: none;
    padding-top: 0;
    margin-top: 0;
  }

  .sidebar__sectionTitle {
    font-size: 11px;
    letter-spacing: 0.4px;
    text-transform: uppercase;
    color: rgba(255, 255, 255, 0.62);
    margin-bottom: 8px;
  }

  .quickadd {
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
  }

  .chips {
    display: flex;
    flex-wrap: wrap;
    gap: 0.5rem;
    margin-top: 0.5rem;
  }

  .chip {
    appearance: none;
    border: 1px solid #30363d;
    background: #21262d;
    color: rgba(255, 255, 255, 0.78);
    border-radius: 999px;
    padding: 0.35rem 0.6rem;
    font-size: 12px;
    cursor: pointer;
    transition: background 120ms ease, border-color 120ms ease;
  }

  .chip:hover {
    background: #30363d;
    border-color: #8b949e;
  }

  .chip--active {
    background: rgba(46, 160, 67, 0.15);
    border-color: #3fb950;
    color: rgba(227, 255, 245, 0.92);
  }

  .input {
    width: 100%;
    min-width: 0;
    border: 1px solid #30363d;
    background: #0d1117;
    color: rgba(255, 255, 255, 0.90);
    border-radius: 10px;
    padding: 9px 10px;
    outline: none;
    font-family: "JetBrains Mono", "Fira Code", monospace;
    font-size: 12px;
  }

  .input--compact {
    padding: 7px 9px;
  }

  .input--select {
    min-width: 100px;
    cursor: pointer;
  }

  .rule__fields {
    margin-top: 0.5rem;
  }

  .field__label {
    font-size: 11px;
    color: rgba(255, 255, 255, 0.52);
    margin-bottom: 0.25rem;
  }

  .input::placeholder {
    color: rgba(255, 255, 255, 0.40);
  }

  .rulelist {
    display: grid;
    gap: 8px;
  }

  .rule {
    display: grid;
    grid-template-columns: 1fr auto;
    gap: 10px;
    align-items: center;
    padding: 10px;
    border-radius: 12px;
    border: 1px solid rgba(255, 255, 255, 0.08);
    background: rgba(0, 0, 0, 0.14);
  }

  .rule__right {
    display: inline-flex;
    align-items: center;
    gap: 8px;
  }

  .rule__name {
    font-size: 12px;
    color: rgba(255, 255, 255, 0.90);
  }

  .rule__desc {
    margin-top: 2px;
    font-size: 11px;
    color: rgba(255, 255, 255, 0.55);
  }

  .toggle {
    display: inline-flex;
    align-items: center;
  }

  .toggle input {
    position: absolute;
    opacity: 0;
    width: 1px;
    height: 1px;
    overflow: hidden;
  }

  .toggle__track {
    width: 44px;
    height: 26px;
    border-radius: 999px;
    border: 1px solid rgba(255, 255, 255, 0.14);
    background: rgba(255, 255, 255, 0.06);
    position: relative;
    transition: background 120ms ease, border-color 120ms ease;
  }

  .toggle__track::after {
    content: '';
    position: absolute;
    top: 3px;
    left: 3px;
    width: 20px;
    height: 20px;
    border-radius: 50%;
    background: rgba(255, 255, 255, 0.78);
    transition: transform 120ms ease, background 120ms ease;
    box-shadow: 0 6px 14px rgba(0, 0, 0, 0.35);
  }

  .toggle input:checked + .toggle__track {
    background: rgba(16, 185, 129, 0.20);
    border-color: rgba(16, 185, 129, 0.35);
  }

  .toggle input:checked + .toggle__track::after {
    transform: translateX(18px);
    background: rgba(227, 255, 245, 0.95);
  }

  .muted {
    font-size: 12px;
    color: rgba(255, 255, 255, 0.55);
  }

  .error {
    font-size: 12px;
    color: rgba(248, 113, 113, 0.95);
  }

  .workspace {
    grid-row: 3;
    grid-column: 1;
    position: relative;
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 20px;
    min-height: 0;
    overflow: hidden;
    padding: 20px;
    width: 100%;
    box-sizing: border-box;
    transition: grid-template-columns 0.25s ease-out, padding 0.25s ease-out;
  }

  /* Satellite: vertical stack, both panels visible (40/60 split) */
  .workspace--satellite.satellite-layout,
  .satellite-layout.workspace--satellite {
    display: flex;
    flex-direction: column;
    grid-template-columns: unset;
    gap: 10px;
    padding: 8px;
  }

  .workspace--satellite .panel--raw {
    flex: 0 0 40%;
    min-height: 120px;
    max-height: 50%;
  }

  .workspace--satellite .panel--raw.panel--raw-hidden {
    display: none;
  }

  .workspace--satellite .panel--safe {
    flex: 1 1 60%;
    min-height: 0;
  }

  .workspace--satellite .panel--raw.panel--raw-hidden ~ .panel--safe {
    flex: 1 1 100%;
  }

  .drop-overlay {
    position: absolute;
    inset: 20px;
    border: 2px dashed rgba(46, 160, 67, 0.6);
    border-radius: 14px;
    background: rgba(22, 27, 34, 0.92);
    display: flex;
    align-items: center;
    justify-content: center;
    font-size: 18px;
    font-weight: 600;
    color: rgba(255, 255, 255, 0.9);
    pointer-events: none;
    z-index: 10;
  }

  .drop-toast {
    position: absolute;
    top: 24px;
    left: 50%;
    transform: translateX(-50%);
    padding: 10px 16px;
    border-radius: 10px;
    background: rgba(248, 81, 73, 0.95);
    color: #fff;
    font-size: 13px;
    font-weight: 500;
    z-index: 11;
    box-shadow: 0 4px 12px rgba(0, 0, 0, 0.3);
    animation: drop-toast-in 0.2s ease-out;
  }

  @keyframes drop-toast-in {
    from {
      opacity: 0;
      transform: translateX(-50%) translateY(-8px);
    }
    to {
      opacity: 1;
      transform: translateX(-50%) translateY(0);
    }
  }

  .library {
    grid-row: 3;
    grid-column: 1;
    min-height: 0;
    overflow: hidden;
    display: flex;
    flex-direction: column;
    padding: 1rem;
    gap: 1rem;
  }

  .library__bar {
    flex-shrink: 0;
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
  }

  .library__search {
    display: flex;
    flex-direction: column;
    gap: 0.25rem;
  }

  .search-wrap {
    display: flex;
    align-items: center;
    gap: 6px;
    position: relative;
  }

  .search-wrap__input {
    flex: 1;
    min-width: 0;
  }

  .search-wrap__clear {
    flex-shrink: 0;
    display: flex;
    align-items: center;
    justify-content: center;
    width: 32px;
    height: 32px;
    padding: 0;
    border: 1px solid #30363d;
    border-radius: 8px;
    background: #21262d;
    color: rgba(255, 255, 255, 0.6);
    cursor: pointer;
    transition: color 120ms ease, background 120ms ease, border-color 120ms ease;
  }

  .search-wrap__clear:hover:not(:disabled) {
    background: #30363d;
    color: rgba(255, 255, 255, 0.9);
    border-color: #8b949e;
  }

  .search-wrap__clear:disabled {
    opacity: 0.4;
    cursor: not-allowed;
  }

  .library__label {
    font-size: 11px;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    color: rgba(255, 255, 255, 0.6);
  }

  .library__quickadd {
    flex-shrink: 0;
    padding: 12px;
    border-radius: 12px;
    border: 1px solid #30363d;
    background: #161b22;
  }

  .library__sectionTitle {
    font-size: 11px;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    color: rgba(255, 255, 255, 0.65);
    margin-bottom: 0.5rem;
  }

  .quickadd--row {
    display: flex;
    flex-wrap: wrap;
    gap: 0.5rem;
    align-items: center;
  }

  .library__bulkadd {
    flex-shrink: 0;
    padding: 12px;
    border-radius: 12px;
    border: 1px solid #30363d;
    background: #161b22;
    display: flex;
    flex-direction: column;
    gap: 8px;
    position: relative;
  }

  .library__bulkadd--dragging {
    border-color: rgba(63, 185, 80, 0.5);
    background: rgba(63, 185, 80, 0.06);
  }

  .bulkadd__dropoverlay {
    position: absolute;
    inset: 0;
    border-radius: 12px;
    border: 2px dashed rgba(63, 185, 80, 0.5);
    background: rgba(22, 27, 34, 0.9);
    display: flex;
    align-items: center;
    justify-content: center;
    font-size: 14px;
    font-weight: 600;
    color: rgba(255, 255, 255, 0.9);
    pointer-events: none;
    z-index: 2;
  }

  .library__hint {
    font-size: 12px;
    color: rgba(255, 255, 255, 0.55);
    margin: 0 0 4px 0;
  }

  .input--bulk {
    min-height: 80px;
    resize: vertical;
    font-family: "JetBrains Mono", "Fira Code", monospace;
    font-size: 12px;
  }

  .bulkadd__row {
    display: flex;
    flex-wrap: wrap;
    gap: 10px;
    align-items: center;
  }

  .bulkadd__row .library__label {
    margin-bottom: 0;
  }

  .library__status {
    font-size: 12px;
    margin: 4px 0 0 0;
  }

  .quickadd--modal {
    display: grid;
    grid-template-columns: 1fr;
    gap: 8px;
    margin-top: 10px;
  }

  .library__group .library__cell--group {
    background: rgba(33, 38, 45, 0.75);
    color: rgba(255, 255, 255, 0.78);
    font-size: 11px;
    letter-spacing: 0.04em;
    text-transform: uppercase;
    border-bottom: 1px solid #30363d;
  }

  /* .quickadd--row .input was removed when Quick Add became a modal */

  .library__table-wrap {
    min-height: 0;
    overflow: auto;
    border: 1px solid #30363d;
    border-radius: 12px;
    background: #161b22;
  }

  .library__table {
    width: 100%;
    table-layout: fixed;
    border-collapse: collapse;
    font-size: 12px;
  }

  .library__col--enabled { width: 80px; }
  .library__col--category { width: 100px; }
  .library__col--name { width: 200px; }
  .library__col--severity { width: 100px; }
  .library__col--pattern { width: 14%; min-width: 90px; }
  .library__col--replacement { width: 180px; }
  .library__col--actions { width: 80px; }

  .library__th {
    box-sizing: border-box;
    text-align: left;
    padding: 10px 12px;
    border-bottom: 1px solid #30363d;
    color: rgba(255, 255, 255, 0.7);
    font-weight: 600;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .library__th--enabled {
    width: 80px;
    min-width: 80px;
    text-align: center;
  }
  .library__th--category {
    width: 100px;
    min-width: 100px;
    text-align: center;
  }
  .library__th--name {
    width: 200px;
    min-width: 200px;
    text-align: center;
  }
  .library__th--severity {
    width: 100px;
    min-width: 100px;
    text-align: center;
  }
  .library__th--pattern {
    width: 14%;
    min-width: 90px;
    text-align: center;
  }
  .library__th--replacement {
    width: 180px;
    min-width: 180px;
    text-align: center;
  }
  .library__th--actions {
    width: 80px;
    min-width: 80px;
    white-space: nowrap;
    text-align: center;
  }

  .library__row {
    border-bottom: 1px solid #30363d;
  }

  .library__row:hover {
    background: rgba(255, 255, 255, 0.03);
  }

  .library__cell {
    box-sizing: border-box;
    padding: 8px 12px;
    vertical-align: middle;
  }

  .library__cell--enabled {
    width: 80px;
    min-width: 80px;
  }
  .library__cell--category {
    width: 100px;
    min-width: 100px;
    text-align: center;
  }
  .library__cell--name {
    width: 200px;
    min-width: 200px;
    text-align: center;
  }
  .library__cell--severity {
    width: 100px;
    min-width: 100px;
    text-align: center;
  }
  .library__cell--pattern {
    width: 14%;
    min-width: 90px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    font-family: ui-monospace, monospace;
    font-size: 11px;
    color: rgba(255, 255, 255, 0.75);
    text-align: center;
  }
  .library__cell--replacement {
    width: 180px;
    min-width: 180px;
    text-align: center;
  }
  .library__cell--actions {
    width: 80px;
    min-width: 80px;
    white-space: nowrap;
    text-align: center;
  }

  .library__cell--center {
    text-align: center;
  }

  .badge {
    display: inline-block;
    padding: 2px 8px;
    border-radius: 999px;
    font-size: 11px;
    font-weight: 600;
    white-space: nowrap;
  }
  .badge--critical {
    background: rgba(248, 81, 73, 0.2);
    color: #f85149;
    border: 1px solid rgba(248, 81, 73, 0.4);
  }
  .badge--high {
    background: rgba(210, 153, 34, 0.2);
    color: #d29922;
    border: 1px solid rgba(210, 153, 34, 0.4);
  }
  .badge--medium {
    background: rgba(110, 118, 129, 0.25);
    color: #8b949e;
    border: 1px solid rgba(110, 118, 129, 0.4);
  }

  .badge--custom {
    background: rgba(56, 189, 248, 0.12);
    color: rgba(125, 211, 252, 0.95);
    border: 1px solid rgba(56, 189, 248, 0.25);
    font-size: 10px;
    padding: 1px 6px;
    font-weight: 650;
    margin-left: 8px;
  }

  .ruleName {
    display: inline-flex;
    align-items: center;
    gap: 0;
  }

  .input--cell {
    min-width: 100px;
    max-width: 180px;
  }

  .panel {
    display: grid;
    grid-template-rows: auto 1fr;
    min-height: 0;
    border: 1px solid #30363d;
    border-radius: 14px;
    background: #161b22;
    overflow: hidden;
  }

  .panel--safe {
    border-color: rgba(46, 160, 67, 0.35);
    background: linear-gradient(180deg, #161b22 0%, rgba(22, 27, 34, 0.98) 100%);
    box-shadow: 0 0 0 1px rgba(46, 160, 67, 0.12) inset;
    transition: border-color 0.2s ease, box-shadow 0.2s ease;
  }

  .panel--autocopied-flash {
    border-color: rgba(46, 160, 67, 0.85);
    box-shadow: 0 0 0 2px rgba(46, 160, 67, 0.4);
  }

  .autocopy-toggle {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    cursor: pointer;
    font-size: 12px;
    color: rgba(255, 255, 255, 0.78);
    user-select: none;
  }

  .autocopy-toggle input {
    margin: 0;
    cursor: pointer;
    accent-color: rgba(46, 160, 67, 0.9);
  }

  .autocopy-toggle__label {
    white-space: nowrap;
  }

  .autocopied-toast {
    font-size: 11px;
    font-weight: 600;
    color: rgba(46, 160, 67, 0.95);
    animation: autocopied-fade 2.2s ease-out;
  }

  @keyframes autocopied-fade {
    0%, 30% { opacity: 1; }
    100% { opacity: 0; }
  }

  .panel__top {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 12px 12px 10px;
    border-bottom: 1px solid #30363d;
  }

  .panel__title {
    font-size: 12px;
    letter-spacing: 0.35px;
    text-transform: uppercase;
    color: rgba(255, 255, 255, 0.70);
  }

  .panel__actions {
    display: flex;
    align-items: center;
    gap: 10px;
  }

  .btn {
    appearance: none;
    border: 1px solid #30363d;
    background: #21262d;
    color: rgba(255, 255, 255, 0.88);
    border-radius: 10px;
    padding: 8px 10px;
    font-size: 12px;
    cursor: pointer;
    transition: background 120ms ease, border-color 120ms ease, transform 120ms ease;
  }

  .btn:hover:enabled {
    background: #30363d;
    border-color: #8b949e;
  }

  .btn:active:enabled {
    transform: translateY(1px);
  }

  .btn:disabled {
    opacity: 0.55;
    cursor: not-allowed;
  }

  .btn--icon {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    padding: 6px 10px;
  }

  .btn__icon {
    flex-shrink: 0;
  }

  .btn__icon-label {
    white-space: nowrap;
  }

  .btn--ghost {
    background: transparent;
  }

  .btn--danger {
    border-color: rgba(239, 68, 68, 0.35);
    background: rgba(239, 68, 68, 0.18);
  }

  .btn--danger:hover:enabled {
    background: rgba(239, 68, 68, 0.24);
    border-color: rgba(239, 68, 68, 0.45);
  }

  .btn--primary {
    border-color: rgba(88, 166, 255, 0.4);
    background: rgba(88, 166, 255, 0.2);
  }

  .btn--primary:hover:enabled {
    background: rgba(88, 166, 255, 0.28);
    border-color: rgba(88, 166, 255, 0.55);
  }

  .copy-status {
    font-size: 12px;
    color: rgba(255, 255, 255, 0.70);
  }

  .panel__textarea-wrap {
    position: relative;
    flex: 1;
    min-height: 0;
    display: flex;
    flex-direction: column;
  }

  .panel__textarea-wrap > .textarea {
    width: 100%;
    flex: 1;
    min-height: 120px;
  }

  /* Mirror sits behind textarea; same font/padding so highlighted text aligns. Textarea becomes transparent when search active so mirror shows through. */
  .highlight-mirror {
    position: absolute;
    inset: 0;
    z-index: 0;
    overflow: auto;
    font-family: "JetBrains Mono", "Fira Code", monospace;
    font-size: 13px;
    line-height: 1.5;
    -webkit-font-smoothing: antialiased;
    color: rgba(255, 255, 255, 0.92);
    pointer-events: none;
    text-align: left;
  }

  .highlight-mirror__content {
    display: block;
    white-space: pre-wrap;
    word-break: break-word;
    padding: 12px;
    box-sizing: border-box;
  }

  .app--satellite .highlight-mirror {
    font-size: 11px;
    line-height: 1.45;
  }

  .app--satellite .highlight-mirror__content {
    padding: 8px;
  }

  .highlight-mirror mark.highlight {
    display: inline;
    padding: 0;
    border-radius: 2px;
  }

  .highlight--raw {
    background: rgba(210, 153, 34, 0.5);
    color: rgba(255, 255, 255, 0.95);
  }

  .highlight--safe {
    background: rgba(46, 160, 67, 0.45);
    color: rgba(255, 255, 255, 0.95);
  }

  /* When search is active, textarea is transparent so the mirror (highlighted text) shows through; caret stays visible */
  .textarea--search-active {
    position: relative;
    z-index: 1;
    background: transparent !important;
    color: transparent !important;
    caret-color: rgba(255, 255, 255, 0.95);
  }

  .textarea--search-active::placeholder {
    color: rgba(255, 255, 255, 0.35);
  }

  .textarea {
    width: 100%;
    height: 100%;
    resize: none;
    border: none;
    outline: none;
    padding: 12px;
    background: transparent;
    color: rgba(255, 255, 255, 0.92);
    font-family: "JetBrains Mono", "Fira Code", monospace;
    font-size: 13px;
    line-height: 1.5;
    -webkit-font-smoothing: antialiased;
    overflow: auto;
  }

  .textarea::placeholder {
    color: rgba(255, 255, 255, 0.48);
  }

  .textarea--readonly {
    color: rgba(255, 255, 255, 0.88);
  }

  .textarea--safe {
    background: rgba(46, 160, 67, 0.04);
  }

  .output-debug {
    white-space: pre-wrap;
    word-break: break-word;
    min-height: 120px;
  }

  .output-redaction {
    cursor: help;
    text-decoration: underline;
    text-decoration-style: dotted;
    text-underline-offset: 2px;
  }

  .output-redaction:hover {
    background: rgba(210, 153, 34, 0.15);
    border-radius: 2px;
  }

  .debug-warning {
    grid-column: 1 / -1;
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    padding: 8px 16px;
    background: rgba(210, 153, 34, 0.12);
    border-bottom: 1px solid rgba(210, 153, 34, 0.35);
    color: rgba(255, 220, 160, 0.95);
    font-size: 12px;
    line-height: 1.4;
    --wails-draggable: no-drag;
  }

  .debug-warning__text {
    flex: 1;
    min-width: 0;
  }

  .debug-warning__dismiss {
    appearance: none;
    border: 1px solid rgba(210, 153, 34, 0.45);
    background: rgba(210, 153, 34, 0.15);
    color: rgba(255, 230, 180, 0.95);
    border-radius: 6px;
    padding: 4px 10px;
    font-size: 11px;
    cursor: pointer;
    flex-shrink: 0;
  }

  .debug-warning__dismiss:hover {
    background: rgba(210, 153, 34, 0.25);
  }

  .debug-console {
    grid-column: 3;
    grid-row: 1 / -1;
    min-width: 0;
    display: flex;
    flex-direction: column;
    border: 1px solid #30363d;
    border-radius: 14px;
    background: #161b22;
    padding: 12px;
    overflow: hidden;
  }

  .debug-console__title {
    font-size: 13px;
    font-weight: 600;
    color: rgba(255, 255, 255, 0.95);
    margin-bottom: 2px;
  }

  .debug-console__subtitle {
    font-size: 11px;
    color: rgba(255, 255, 255, 0.5);
    margin-bottom: 10px;
  }

  .debug-console__empty {
    font-size: 12px;
    color: rgba(255, 255, 255, 0.45);
    margin: 0;
  }

  .debug-console__list {
    list-style: none;
    margin: 0;
    padding: 0;
    overflow: auto;
    display: flex;
    flex-direction: column;
    gap: 6px;
  }

  .debug-console__item {
    font-size: 12px;
    padding: 6px 8px;
    border-radius: 8px;
    background: rgba(255, 255, 255, 0.05);
    border: 1px solid rgba(255, 255, 255, 0.08);
    display: flex;
    flex-direction: column;
    gap: 2px;
  }

  .debug-console__rule {
    color: rgba(255, 255, 255, 0.9);
    font-weight: 500;
  }

  .debug-console__repl {
    font-size: 11px;
    color: rgba(255, 255, 255, 0.55);
    font-family: "JetBrains Mono", monospace;
  }

  .footer {
    grid-column: 1 / -1;
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 12px 16px;
    background: #161b22;
    border-top: 1px solid #30363d;
    color: rgba(255, 255, 255, 0.62);
    font-size: 12px;
    --wails-draggable: drag;
  }

  .footer button,
  .footer input,
  .footer select,
  .footer label {
    --wails-draggable: no-drag;
  }

  .footer__left {
    display: flex;
    align-items: center;
    gap: 16px;
  }

  .footer__toggle {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    cursor: pointer;
    user-select: none;
  }

  .footer__toggle input {
    accent-color: #58a6ff;
  }

  .footer__toggleLabel {
    color: rgba(255, 255, 255, 0.72);
  }

  .ghost-toast {
    position: fixed;
    left: 50%;
    bottom: 28%;
    transform: translateX(-50%);
    padding: 10px 18px;
    border-radius: 8px;
    background: #21262d;
    border: 1px solid #3fb950;
    color: #3fb950;
    font-size: 13px;
    font-weight: 500;
    z-index: 9998;
    pointer-events: none;
    animation: ghost-toast-in 0.25s ease-out forwards;
    box-shadow: 0 4px 12px rgba(0, 0, 0, 0.35);
  }

  @keyframes ghost-toast-in {
    from {
      opacity: 0;
      transform: translateX(-50%) translateY(8px);
    }
    to {
      opacity: 1;
      transform: translateX(-50%) translateY(0);
    }
  }

  .clipboard-wipe-toast {
    position: fixed;
    left: 50%;
    bottom: 56px;
    transform: translateX(-50%);
    padding: 8px 16px;
    border-radius: 8px;
    background: #21262d;
    border: 1px solid rgba(88, 166, 255, 0.45);
    color: #58a6ff;
    font-size: 13px;
    font-weight: 600;
    font-variant-numeric: tabular-nums;
    z-index: 9997;
    pointer-events: none;
    box-shadow: 0 4px 12px rgba(0, 0, 0, 0.35);
    animation: ghost-toast-in 0.25s ease-out forwards;
    white-space: nowrap;
  }

  .footer__prompt-wrap {
    display: inline-flex;
    align-items: center;
    gap: 6px;
  }

  .footer__prompt-label {
    font-size: 11px;
    color: rgba(255, 255, 255, 0.55);
    white-space: nowrap;
  }

  .footer__prompt-select {
    padding: 4px 8px;
    border-radius: 6px;
    border: 1px solid #30363d;
    background: #21262d;
    color: rgba(255, 255, 255, 0.88);
    font-size: 11px;
    cursor: pointer;
    min-width: 0;
  }

  .footer__right {
    display: flex;
    align-items: center;
    gap: 12px;
  }

  .footer__stats {
    display: inline-flex;
    align-items: center;
    gap: 8px;
    padding: 6px 12px;
    border-radius: 999px;
    border: 1px solid #30363d;
    background: #21262d;
    font-size: 12px;
    color: rgba(255, 255, 255, 0.82);
  }

  .footer__stat {
    white-space: nowrap;
  }

  .footer__statIcon {
    margin-right: 4px;
    opacity: 0.9;
    font-size: 10px;
  }

  .footer__statIcon--shield {
    font-size: 1em;
  }

  .footer__stat--customActive {
    color: #d29922;
  }

  .footer__statDivider {
    opacity: 0.5;
    user-select: none;
  }

  .library__footer {
    flex-shrink: 0;
    padding-top: 0.5rem;
    border-top: 1px solid #30363d;
  }

  .btn--secondary {
    border-color: rgba(255, 255, 255, 0.15);
    color: rgba(255, 255, 255, 0.8);
  }

  .btn--secondary:hover:enabled {
    background: rgba(255, 255, 255, 0.08);
    border-color: rgba(255, 255, 255, 0.2);
  }

  .btn--full {
    width: 100%;
    justify-content: center;
  }

  .status {
    display: inline-flex;
    align-items: center;
    gap: 8px;
    padding: 6px 10px;
    border-radius: 999px;
    border: 1px solid #30363d;
    background: #161b22;
    color: rgba(227, 255, 245, 0.92);
  }

  .status__dot {
    width: 8px;
    height: 8px;
    border-radius: 50%;
    background: #3fb950;
    box-shadow: 0 0 0 2px rgba(63, 185, 80, 0.25);
  }

  .status--button {
    cursor: pointer;
    border: 1px solid #30363d;
    background: #161b22;
    font: inherit;
    color: inherit;
    transition: background 120ms ease, border-color 120ms ease;
  }

  .status--button:hover {
    background: #21262d;
    border-color: rgba(63, 185, 80, 0.35);
  }

  .modal--security .modal__body--security {
    display: flex;
    flex-direction: column;
    gap: 16px;
  }

  .security-block {
    padding: 12px;
    border-radius: 10px;
    background: rgba(0, 0, 0, 0.2);
    border: 1px solid rgba(255, 255, 255, 0.06);
  }

  .security-block__label {
    font-size: 11px;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    color: rgba(255, 255, 255, 0.5);
    margin-bottom: 6px;
  }

  .security-block__value {
    font-size: 13px;
    color: rgba(255, 255, 255, 0.88);
    line-height: 1.45;
  }

  .security-block__value--big {
    font-size: 18px;
    font-weight: 600;
    color: rgba(63, 185, 80, 0.95);
  }

  .security-block__value--muted {
    font-size: 12px;
    color: rgba(255, 255, 255, 0.52);
  }

  .security-block--heartbeat {
    border-color: rgba(63, 185, 80, 0.2);
    background: rgba(63, 185, 80, 0.06);
  }

  @media (max-width: 980px) {
    .workspace {
      grid-template-columns: 1fr;
    }
  }

  .modalOverlay {
    position: fixed;
    inset: 0;
    background: rgba(0, 0, 0, 0.62);
    display: grid;
    place-items: center;
    padding: 18px;
    border: none;
    cursor: pointer;
  }

  .modal {
    width: min(560px, 100%);
    border-radius: 16px;
    border: 1px solid #30363d;
    background: #161b22;
    box-shadow: 0 30px 80px rgba(0, 0, 0, 0.55);
    padding: 16px;
  }

  .modal__title {
    font-size: 14px;
    font-weight: 650;
    margin-bottom: 8px;
  }

  .modal__body {
    font-size: 13px;
    color: rgba(255, 255, 255, 0.78);
    line-height: 1.4;
  }

  .modal__actions {
    display: flex;
    justify-content: flex-end;
    gap: 10px;
    margin-top: 14px;
  }

  .settings__lead {
    margin: 0 0 12px;
    font-size: 14px;
    line-height: 1.5;
    color: rgba(255, 255, 255, 0.85);
  }

  .settings__meta {
    margin: 0 0 14px;
    font-size: 13px;
    color: rgba(255, 255, 255, 0.6);
  }

  .settings__version {
    font-family: ui-monospace, monospace;
    color: rgba(255, 255, 255, 0.82);
  }

  .settings__link {
    display: inline-block;
    font-size: 14px;
    color: #00A3A3;
    text-decoration: underline;
    text-underline-offset: 2px;
  }

  .settings__link:hover {
    color: #33c4c4;
  }

  .header__about-btn {
    appearance: none;
    border: none;
    padding: 6px;
    margin-right: 4px;
    display: flex;
    align-items: center;
    justify-content: center;
    background: transparent;
    color: rgba(255, 255, 255, 0.7);
    cursor: pointer;
    border-radius: 8px;
    transition: color 0.15s ease, background 0.15s ease;
  }

  .header__about-btn:hover {
    color: #9ea0a0;
    background: rgba(255, 255, 255, 0.06);
  }

  .header__settings-btn {
    appearance: none;
    border: none;
    padding: 6px;
    margin-right: 4px;
    display: flex;
    align-items: center;
    justify-content: center;
    background: transparent;
    color: rgba(255, 255, 255, 0.7);
    cursor: pointer;
    border-radius: 8px;
    transition: color 0.15s ease, background 0.15s ease;
    --wails-draggable: no-drag;
  }

  .header__settings-btn:hover {
    color: #9ea0a0;
    background: rgba(255, 255, 255, 0.06);
  }

  .header__settings-icon {
    flex-shrink: 0;
  }

  .header__about-icon {
    flex-shrink: 0;
  }

  .modal--about {
    background: #161b22;
    border: 1px solid #9ea0a0;
    width: min(640px, 96vw);
    max-width: 640px;
    max-height: calc(100vh - 36px);
    display: flex;
    flex-direction: column;
    font-family: "Inter", sans-serif;
  }

  .modal__body--about-scroll {
    overflow-y: auto;
    flex: 1;
    min-height: 0;
    max-height: min(72vh, 640px);
    padding-right: 4px;
  }

  .modal__title--about {
    color: #9ea0a0;
    font-size: 18px;
    font-weight: 600;
    margin-bottom: 12px;
  }

  .modal__body--about {
    color: rgba(255, 255, 255, 0.88);
    font-size: 14px;
    line-height: 1.5;
  }

  .modal__actions--about {
    justify-content: flex-end;
    margin-top: 16px;
  }

  .btn--about-close {
    background: rgba(158, 160, 160, 0.15);
    border: 1px solid #9ea0a0;
    color: #9ea0a0;
  }

  .btn--about-close:hover {
    background: rgba(158, 160, 160, 0.25);
    color: rgba(255, 255, 255, 0.9);
  }
</style>
