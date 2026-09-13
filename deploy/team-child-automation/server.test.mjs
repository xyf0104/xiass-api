import assert from 'node:assert/strict'
import fs from 'node:fs'
import path from 'node:path'
import { describe, it } from 'node:test'
import { fileURLToPath } from 'node:url'

process.env.NODE_ENV = 'test'
process.env.TEAM_CHILD_AUTOMATION_TOKEN = 'unit-test-team-child-token'

const {
  activateBrowserPage,
  advanceRegisteredOAuth,
  advanceOAuthReauthorization,
  beginWorkflowEmailChallenge,
  callbackURLFromNavigationEntries,
  cancelWorkflowState,
  confirmOfficialMemberRemoval,
  completeReauthorizationOnlyNodes,
  completeWorkflowNode,
  createPrivateBrowserSession,
  createReauthorizationWorkflow,
  createWorkflow,
  clickOAuthContinue,
  decryptWorkflowState,
  encryptWorkflowState,
  fillLoginPassword,
  fillVerificationCode,
  fillWorkflowEmail,
  markWorkflowInviteSubmitted,
  pauseWorkflowState,
  pendingInviteEmailsFromTexts,
  pendingInviteRecord,
  pendingInvitesTabSelected,
  isSignupAccountCreationRejectionText,
  registeredOAuthNextState,
  reauthorizationNextState,
  recoverOpenAIPhoneEntry,
  resetOAuthWorkflowSteps,
  reusableOAuthPage,
  resumePausedWorkflow,
  selectLoginForAnotherAccount,
  setWorkflowNode,
  submitInviteDialog,
  validateOAuthSessionID,
  validateWorkflowCode,
  workflowProtocolVersion,
  workflowNodeDefinitions,
  workflowFailureNodeKey,
  workflowSummary
} = await import('./server.mjs')

const authURL = 'https://auth.openai.com/oauth/authorize?client_id=app_EMoamEEZ73f0CkXaXp7hrann&code_challenge=test-challenge&code_challenge_method=S256&codex_cli_simplified_flow=true&id_token_add_organizations=true&redirect_uri=http%3A%2F%2Flocalhost%3A1455%2Fauth%2Fcallback&response_type=code&scope=openid+profile+email+offline_access&state=test-state'
const automationDirectory = path.dirname(fileURLToPath(import.meta.url))
const deployDirectory = path.dirname(automationDirectory)

describe('Team child OAuth automation state', () => {
  it('keeps the 401 authenticator secret out of snapshots and preserves it only until completion or cancel', () => {
    const workflow = createReauthorizationWorkflow(91, 'account@example.test', 'synthetic-password', authURL, 'oauth-session-abcdefghijklmnop', 'JBSWY3DPEHPK3PXP')
    assert.equal(workflow.loginTOTPSecret, 'JBSWY3DPEHPK3PXP')
    assert.equal(JSON.stringify(workflow).includes('JBSWY3DPEHPK3PXP'), false)
    assert.equal(JSON.stringify(workflowSummary(workflow)).includes('loginTOTPSecret'), false)
    workflow.totpSubmitted = true
    resetOAuthWorkflowSteps(workflow)
    assert.equal(workflow.totpSubmitted, false)
    assert.equal(workflow.loginTOTPSecret, 'JBSWY3DPEHPK3PXP')
    cancelWorkflowState(workflow)
    assert.equal(workflow.loginTOTPSecret, '')
  })
  it('activates managed pages without requiring the noVNC viewer', async () => {
    const sent = []
    let broughtToFront = 0
    let detached = 0
    const cdpSession = {
      send: async (method, params) => { sent.push([method, params]) },
      detach: async () => { detached += 1 }
    }
    const page = {
      bringToFront: async () => { broughtToFront += 1 },
      context: () => ({ newCDPSession: async () => cdpSession })
    }

    await activateBrowserPage(page)

    assert.equal(broughtToFront, 1)
    assert.deepEqual(sent, [
      ['Page.bringToFront', undefined],
      ['Emulation.setFocusEmulationEnabled', { enabled: true }]
    ])
    assert.equal(detached, 1)
  })

  it('creates a genuinely isolated browser context for a new Team identity', async () => {
    const page = { id: 'private-page' }
    let contextCreated = 0
    let pageCreated = 0
    const context = {
      newPage: async () => {
        pageCreated += 1
        return page
      },
      close: async () => undefined
    }
    const connected = {
      newContext: async () => {
        contextCreated += 1
        return context
      }
    }

    assert.deepEqual(await createPrivateBrowserSession(connected), { context, page })
    assert.equal(contextCreated, 1)
    assert.equal(pageCreated, 1)
  })

  it('publishes only the current 22-node workflow protocol', () => {
    const workflow = createWorkflow('member@example.test', 'child@example.test', authURL, 'oauth-session-abcdefghijklmnop', false)
    const summary = workflowSummary(workflow)
    assert.equal(workflowProtocolVersion, 4)
    assert.equal(summary.schema_version, 4)
    assert.equal(summary.nodes.length, 22)
    assert.equal('startStep' in workflow, false)
    assert.equal('runOnlyStep' in workflow, false)
    assert.equal('resumeNextStepIndex' in workflow, false)
  })

  it('keeps every deployment health check pinned to the current workflow protocol', () => {
    for (const filename of ['docker-compose.yml', 'docker-compose.local.yml', 'docker-compose.standalone.yml', 'docker-compose.dev.yml']) {
      const compose = fs.readFileSync(path.join(deployDirectory, filename), 'utf8')
      assert.match(compose, /x-xiass-team-child-protocol'\) === '4'/)
      assert.match(compose, /workflow_schema_version === 4/)
      assert.doesNotMatch(compose, /x-xiass-team-child-protocol'\) === '3'/)
    }

    const runtimeStart = fs.readFileSync(path.join(deployDirectory, 'xiass-runtime-start.sh'), 'utf8')
    assert.match(runtimeStart, /x-xiass-team-child-protocol'\) === '4'/)
    assert.match(runtimeStart, /body\.workflow_schema_version === 4/)
    assert.match(runtimeStart, /\[ "\$protocol" != "4" \]/)
  })

  it('extracts invitations only from supplied pending-record text', () => {
    assert.deepEqual([...pendingInviteEmailsFromTexts(['No results'])], [])
    assert.deepEqual(
      [...pendingInviteEmailsFromTexts(['child@example.test\nInvited today\nMember'])],
      ['child@example.test']
    )
  })

  it('recognizes the native Pending invites button and matches only the exact invitation row', async () => {
    const collection = (items) => ({
      count: async () => items.length,
      nth: (index) => items[index]
    })
    const selectedTab = {
      innerText: async () => 'Pending invites',
      getAttribute: async (name) => name === 'class'
        ? 'text-token-text-primary border-token-text-secondary border-b'
        : null
    }
    const rows = [
      { innerText: async () => 'other@example.test\nSep 7, 2026\nMember' },
      { innerText: async () => 'failed@example.test\nSep 7, 2026\nMember' }
    ]
    const page = {
      locator: (selector) => selector.startsWith('button:visible')
        ? collection([selectedTab])
        : selector.startsWith('table:visible tbody tr') ? collection(rows) : collection([])
    }

    assert.equal(await pendingInvitesTabSelected(page), true)
    const record = await pendingInviteRecord(page, 'FAILED@example.test')
    assert.equal(record.row, rows[1])
    assert.equal(record.email, 'failed@example.test')
  })

  it('detects confirmed account-creation rejection without treating ordinary signup text as failure', () => {
    assert.equal(isSignupAccountCreationRejectionText("We couldn't create your account. Please try again later."), true)
    assert.equal(isSignupAccountCreationRejectionText('无法创建您的账户，请稍后重试'), true)
    assert.equal(isSignupAccountCreationRejectionText('Create your account to continue'), false)
    assert.equal(isSignupAccountCreationRejectionText('Loading your account'), false)
  })

  it('never selects the ChatGPT Members tab as the OAuth workflow tab', () => {
    const page = (url) => ({ url: () => url, isClosed: () => false })
    const members = page('https://chatgpt.com/admin/members')
    const oauth = page('https://auth.openai.com/oauth/authorize?state=test-state')

    assert.equal(reusableOAuthPage({ pages: () => [members] }), undefined)
    assert.equal(reusableOAuthPage({ pages: () => [members, oauth] }), oauth)
  })

  it('classifies each official reauthorization page before taking the next action', async () => {
    const collection = (items) => ({
      count: async () => items.length,
      nth: (index) => items[index]
    })
    const input = (attributes = {}) => ({
      isVisible: async () => true,
      getAttribute: async (name) => attributes[name] || null
    })
    const page = ({ url, body = '', inputs = [], verification = [] }) => ({
      url: () => url,
      locator: (selector) => {
        if (selector === 'body') return { innerText: async () => body }
        if (selector === 'input') return collection(inputs)
        if (selector.includes('one-time-code')) return collection(verification)
        return collection([])
      },
      getByRole: () => collection([])
    })
    const workflow = createReauthorizationWorkflow(317, 'child@example.test', '', authURL, 'oauth-session-abcdefghijklmnop')

    assert.equal((await reauthorizationNextState(page({
      url: 'https://auth.openai.com/log-in',
      body: 'Log in or sign up',
      inputs: [input({ type: 'email', autocomplete: 'username' })]
    }), workflow)).kind, 'email')
    assert.equal((await reauthorizationNextState(page({
      url: 'https://auth.openai.com/log-in/password',
      inputs: [input({ type: 'password', autocomplete: 'current-password' })]
    }), workflow)).kind, 'password')
    assert.equal((await reauthorizationNextState(page({
      url: 'https://auth.openai.com/verify',
      body: 'Check your inbox for the verification code',
      verification: [input({ autocomplete: 'one-time-code' })]
    }), workflow)).kind, 'email_code')
    assert.equal((await reauthorizationNextState(page({
      url: 'https://auth.openai.com/sign-in-with-chatgpt/codex/consent',
      body: 'Select a workspace Continue'
    }), workflow)).kind, 'workspace')
    assert.equal((await reauthorizationNextState(page({
      url: 'https://auth.openai.com/choose-an-account',
      body: 'Welcome back Choose an account to continue to Codex Log in to another account'
    }), workflow)).kind, 'account_chooser')
    assert.equal((await reauthorizationNextState(page({
      url: 'https://accounts.google.com/signin'
    }), workflow)).kind, 'external_provider')
  })

  it('recognizes the OAuth login and phone pages used by a newly registered private session', async () => {
    const collection = (items) => ({
      count: async () => items.length,
      nth: (index) => items[index]
    })
    const input = (attributes = {}) => ({
      isVisible: async () => true,
      getAttribute: async (name) => attributes[name] || null
    })
    const page = ({ url, body, inputs = [] }) => ({
      url: () => url,
      locator: (selector) => {
        if (selector === 'body') return { innerText: async () => body }
        if (selector === 'input') return collection(inputs)
        return collection([])
      },
      getByRole: () => collection([])
    })
    const workflow = createWorkflow('member@example.test', 'child@example.test', authURL, 'oauth-session-abcdefghijklmnop', false)

    assert.equal((await registeredOAuthNextState(page({
      url: 'https://auth.openai.com/log-in',
      body: 'Welcome back Email address Continue',
      inputs: [input({ type: 'email', autocomplete: 'username' })]
    }), workflow)).kind, 'email')
    assert.equal((await registeredOAuthNextState(page({
      url: 'https://auth.openai.com/add-phone',
      body: 'Phone number required Add your phone number to continue',
      inputs: [input({ type: 'tel', autocomplete: 'tel' })]
    }), workflow)).kind, 'phone')
  })

  it('recognizes the personal workspace page used by ordinary 401 reauthorization', async () => {
    const collection = (items) => ({ count: async () => items.length, nth: (index) => items[index] })
    const page = {
      url: () => 'https://auth.openai.com/oauth/authorize',
      locator: (selector) => selector === 'body'
        ? { innerText: async () => 'Personal workspace Continue' }
        : collection([]),
      getByRole: () => collection([])
    }
    const workflow = createReauthorizationWorkflow(317, 'child@example.test', 'SavedPassword!', authURL, 'oauth-session-abcdefghijklmnop')

    assert.equal((await reauthorizationNextState(page, workflow)).kind, 'workspace')
  })

  it('submits the same mailbox on OAuth login, verifies the second code, and reaches phone entry', async () => {
    let screen = 'email'
    let submittedEmail = ''
    let submittedCode = ''
    const collection = (items) => ({
      count: async () => items.length,
      nth: (index) => items[index]
    })
    const emailInput = {
      isVisible: async () => true,
      getAttribute: async (name) => ({ type: 'email', autocomplete: 'username' })[name] || null,
      fill: async (value) => { submittedEmail = value }
    }
    const codeInput = {
      isVisible: async () => true,
      getAttribute: async (name) => ({ type: 'text', autocomplete: 'one-time-code' })[name] || null,
      fill: async (value) => { submittedCode = value }
    }
    const phoneInput = {
      isVisible: async () => true,
      getAttribute: async (name) => ({ type: 'tel', autocomplete: 'tel' })[name] || null
    }
    const continueButton = {
      isVisible: async () => true,
      click: async () => { screen = screen === 'email' ? 'email_code' : 'phone' }
    }
    const page = {
      url: () => screen === 'email'
        ? 'https://auth.openai.com/log-in'
        : screen === 'email_code'
          ? 'https://auth.openai.com/email-verification'
          : 'https://auth.openai.com/add-phone',
      locator: (selector) => {
        if (selector === 'body') return { innerText: async () => screen === 'email'
          ? 'Welcome back Email address'
          : screen === 'email_code'
            ? 'Check your inbox for a verification code'
            : 'Phone number required Add your phone number to continue' }
        if (selector === 'input') {
          if (screen === 'email') return collection([emailInput])
          if (screen === 'email_code') return collection([codeInput])
          return collection([phoneInput])
        }
        if (selector.includes('one-time-code')) return collection(screen === 'email_code' ? [codeInput] : [])
        return collection([])
      },
      getByRole: (role, options) => collection(
        role === 'button' && ['email', 'email_code'].includes(screen) && options.name.test('Continue') ? [continueButton] : []
      )
    }
    const workflow = createWorkflow('member@example.test', 'child@example.test', authURL, 'oauth-session-abcdefghijklmnop', false)

    await advanceRegisteredOAuth(workflow, page, { kind: 'email', input: emailInput })

    assert.equal(submittedEmail, 'child@example.test')
    assert.equal(workflow.oauthEmailSubmitted, true)
    assert.equal(workflow.emailCodePurpose, 'oauth_login')
    assert.equal(workflow.emailCodeGeneration, 1)
    assert.equal(workflow.currentNodeKey, 'password')
    assert.equal(workflow.status, 'manual_required')

    await fillVerificationCode(page, '123456')
    await advanceRegisteredOAuth(workflow, page, await registeredOAuthNextState(page, workflow))

    assert.equal(submittedCode, '123456')
    assert.equal(workflow.nodes.find((node) => node.key === 'password').status, 'completed')
    assert.equal(workflow.nodes.find((node) => node.key === 'phone').status, 'completed')
    assert.equal(workflow.currentNodeKey, 'sms_confirm')
    assert.equal(workflow.status, 'manual_required')
  })

  it('numbers registration and OAuth mailbox challenges as separate generations', () => {
    const workflow = createWorkflow('member@example.test', 'child@example.test', authURL, 'oauth-session-abcdefghijklmnop', false)

    beginWorkflowEmailChallenge(workflow, 'registration', 'mailbox', '等待注册验证码')
    assert.equal(workflowSummary(workflow).email_code_generation, 1)
    assert.equal(workflowSummary(workflow).email_code_purpose, 'registration')

    beginWorkflowEmailChallenge(workflow, 'oauth_login', 'password', '等待 OAuth 登录验证码')
    assert.equal(workflowSummary(workflow).email_code_generation, 2)
    assert.equal(workflowSummary(workflow).email_code_purpose, 'oauth_login')
  })

  it('uses another-account login for a trusted browser session without clicking the saved account', async () => {
    let screen = 'chooser'
    let savedAccountClicks = 0
    let otherAccountClicks = 0
    const collection = (items) => ({
      count: async () => items.length,
      nth: (index) => items[index]
    })
    const emailInput = {
      isVisible: async () => true,
      getAttribute: async (name) => ({ type: 'email', autocomplete: 'username' })[name] || null
    }
    const savedAccount = {
      label: 'remembered@example.test',
      isVisible: async () => true,
      innerText: async () => 'remembered@example.test',
      click: async () => { savedAccountClicks += 1 }
    }
    const otherAccount = {
      label: 'Log in to another account',
      isVisible: async () => true,
      innerText: async () => 'Log in to another account',
      click: async () => {
        otherAccountClicks += 1
        screen = 'email'
      }
    }
    const current = {
      url: () => screen === 'chooser'
        ? 'https://auth.openai.com/choose-an-account'
        : 'https://auth.openai.com/log-in',
      locator: (selector) => {
        if (selector === 'body') {
          return {
            innerText: async () => screen === 'chooser'
              ? 'Welcome back Choose an account to continue to Codex Log in to another account'
              : 'Log in or sign up'
          }
        }
        if (selector === 'input') return collection(screen === 'email' ? [emailInput] : [])
        return collection([])
      },
      getByRole: (role, options) => {
        if (role !== 'button' || screen !== 'chooser') return collection([])
        return collection([savedAccount, otherAccount].filter((candidate) => options.name.test(candidate.label)))
      }
    }
    const workflow = createReauthorizationWorkflow(317, 'child@example.test', '', authURL, 'oauth-session-abcdefghijklmnop')

    const state = await selectLoginForAnotherAccount(current, workflow)

    assert.equal(state.kind, 'email')
    assert.equal(otherAccountClicks, 1)
    assert.equal(savedAccountClicks, 0)
  })

  it('opens a normal Sign in page only when an email field is not yet available', async () => {
    let screen = 'landing'
    let signInClicks = 0
    const collection = (items) => ({
      count: async () => items.length,
      nth: (index) => items[index]
    })
    const emailInput = {
      isVisible: async () => true,
      getAttribute: async (name) => ({ type: 'email', autocomplete: 'username' })[name] || null
    }
    const signIn = {
      isVisible: async () => true,
      innerText: async () => 'Sign in',
      click: async () => {
        signInClicks += 1
        screen = 'email'
      }
    }
    const current = {
      url: () => 'https://auth.openai.com/log-in',
      locator: (selector) => {
        if (selector === 'body') return { innerText: async () => screen === 'landing' ? 'Welcome back' : 'Log in or sign up' }
        if (selector === 'input') return collection(screen === 'email' ? [emailInput] : [])
        return collection([])
      },
      getByRole: (role, options) => collection(
        role === 'button' && screen === 'landing' && options.name.test('Sign in') ? [signIn] : []
      )
    }
    const workflow = createReauthorizationWorkflow(317, 'child@example.test', '', authURL, 'oauth-session-abcdefghijklmnop')

    const state = await selectLoginForAnotherAccount(current, workflow)

    assert.equal(state.kind, 'email')
    assert.equal(signInClicks, 1)
  })

  it('never clicks a third-party identity-provider option', async () => {
    let googleClicks = 0
    const google = {
      isVisible: async () => true,
      click: async () => { googleClicks += 1 },
      innerText: async () => 'Continue with Google'
    }
    const collection = (items) => ({
      count: async () => items.length,
      nth: (index) => items[index]
    })
    const page = {
      url: () => 'https://auth.openai.com/log-in',
      locator: (selector) => selector === 'body'
        ? { innerText: async () => 'Choose a sign-in method' }
        : collection([]),
      getByRole: (role, options) => collection(
        (role === 'button' && options.name.test('Continue with Google')) ? [google] : []
      )
    }
    const workflow = createReauthorizationWorkflow(317, 'child@example.test', '', authURL, 'oauth-session-abcdefghijklmnop')
    const state = await selectLoginForAnotherAccount(page, workflow)

    assert.equal(state.kind, 'external_provider_choice')
    assert.equal(googleClicks, 0)
  })

  it('pauses rather than authorizing a pre-existing workspace session', async () => {
    const workflow = createReauthorizationWorkflow(317, 'child@example.test', '', authURL, 'oauth-session-abcdefghijklmnop')

    await advanceOAuthReauthorization(workflow, {}, { kind: 'workspace' })

    assert.equal(workflow.status, 'manual_required')
    assert.equal(workflow.currentNodeKey, 'signup')
    assert.equal(workflow.nodes.find((node) => node.key === 'email')?.status, 'pending')
    assert.match(workflow.nodes.find((node) => node.key === 'signup')?.message || '', /非目标账号/)
  })

  it('allows a passwordless account and waits only if the official page asks for a password', async () => {
    const workflow = createReauthorizationWorkflow(317, 'child@example.test', '', authURL, 'oauth-session-abcdefghijklmnop')
    workflow.reauthorizationEmailSubmitted = true

    await advanceOAuthReauthorization(workflow, {}, { kind: 'password' }, { operatorConfirmed: true })

    assert.equal(workflow.status, 'manual_required')
    assert.equal(workflow.currentNodeKey, 'password')
    assert.match(workflow.nodes.find((node) => node.key === 'password')?.message || '', /未保存密码/)
  })

  it('reacquires replaced email, password and authenticator controls during 401 reauthorization', async () => {
    let screen = 'email'
    const filled = { email: '', password: '', totp: '' }
    const inputAttempts = { email: 0, password: 0, totp: 0 }
    const buttonAttempts = { email: 0, password: 0, totp: 0 }
    const collection = (items) => ({
      count: async () => items.length,
      nth: (index) => items[index]
    })
    const input = (kind) => {
      const stale = inputAttempts[kind]++ === 0
      const attributes = kind === 'email'
        ? { type: 'email', autocomplete: 'username' }
        : kind === 'password' ? { type: 'password', autocomplete: 'current-password' } : { name: 'code', autocomplete: 'one-time-code' }
      return {
        isVisible: async () => true,
        getAttribute: async (name) => attributes[name] || null,
        fill: async (value) => {
          if (stale) throw new Error('locator.fill: element was detached')
          filled[kind] = value
        }
      }
    }
    const button = () => {
      const stage = screen
      const stale = buttonAttempts[stage]++ === 0
      return {
        isVisible: async () => true,
        click: async () => {
          if (stale) throw new Error('locator.click: element was detached')
          screen = stage === 'email' ? 'password' : stage === 'password' ? 'totp' : 'callback'
        }
      }
    }
    const page = {
      url: () => screen === 'callback'
        ? 'http://localhost:1455/auth/callback?code=test-code&state=test-state'
        : 'https://auth.openai.com/log-in',
      locator: (selector) => {
        if (selector === 'body') {
          return { innerText: async () => ({ email: 'Log in', password: 'Enter your password', totp: 'Enter the code from your authenticator app', callback: '' })[screen] }
        }
        if (selector === 'input') return collection(['email', 'password'].includes(screen) ? [input(screen)] : [])
        if (selector.includes('one-time-code')) return collection(screen === 'totp' ? [input('totp')] : [])
        return collection([])
      },
      getByRole: (role, options) => collection(
        role === 'button' && ['email', 'password', 'totp'].includes(screen) && options.name.test('Continue') ? [button()] : []
      )
    }
    const workflow = createReauthorizationWorkflow(
      317,
      'child@example.test',
      'SavedPassword!',
      authURL,
      'oauth-session-abcdefghijklmnop',
      'JBSWY3DPEHPK3PXP'
    )
    const staleClassifiedInput = { fill: async () => { throw new Error('classified locator must not be reused') } }

    await advanceOAuthReauthorization(workflow, page, { kind: 'email', input: staleClassifiedInput })

    assert.equal(filled.email, 'child@example.test')
    assert.equal(filled.password, 'SavedPassword!')
    assert.match(filled.totp, /^\d{6}$/)
    assert.equal(workflow.status, 'callback_ready')
    assert.equal(workflow.callbackURL, 'http://localhost:1455/auth/callback?code=test-code&state=test-state')
    assert.ok(inputAttempts.email >= 2)
    assert.ok(inputAttempts.password >= 2)
    assert.ok(inputAttempts.totp >= 2)
  })

  it('reacquires a replaced workspace Continue button without clicking the next page', async () => {
    let screen = 'workspace'
    let attempts = 0
    const collection = (items) => ({ count: async () => items.length, nth: (index) => items[index] })
    const page = {
      getByRole: (role, options) => collection(role === 'button' && screen === 'workspace' && options.name.test('Continue') ? [{
        isVisible: async () => true,
        click: async () => {
          attempts += 1
          if (attempts === 1) throw new Error('locator.click: element was detached')
          screen = 'callback'
        }
      }] : [])
    }

    await clickOAuthContinue(page, async () => screen === 'callback')

    assert.equal(screen, 'callback')
    assert.equal(attempts, 2)
  })

  it('reacquires replaced standalone email and password controls', async () => {
    for (const kind of ['email', 'password']) {
      let inputAttempts = 0
      let buttonAttempts = 0
      let value = ''
      let advanced = false
      const collection = (items) => ({ count: async () => items.length, nth: (index) => items[index] })
      const page = {
        locator: (selector) => collection(selector === 'input' && !advanced ? [{
          isVisible: async () => true,
          getAttribute: async (name) => name === 'type' ? kind : null,
          fill: async (next) => {
            inputAttempts += 1
            if (inputAttempts === 1) throw new Error('locator.fill: element was detached')
            value = next
          }
        }] : []),
        getByRole: (role, options) => collection(role === 'button' && !advanced && options.name.test('Continue') ? [{
          isVisible: async () => true,
          click: async () => {
            buttonAttempts += 1
            if (buttonAttempts === 1) throw new Error('locator.click: element was detached')
            advanced = true
          }
        }] : [])
      }

      if (kind === 'email') await fillWorkflowEmail(page, 'person@example.test')
      else await fillLoginPassword(page, 'SavedPassword!')

      assert.equal(value, kind === 'email' ? 'person@example.test' : 'SavedPassword!')
      assert.equal(advanced, true)
      assert.equal(inputAttempts, 2)
      assert.equal(buttonAttempts, 2)
    }
  })

  it('fills the native invite Email input and clicks Send invites', async () => {
    const observed = { email: '', clicked: 0 }
    const input = {
      isVisible: async () => true,
      getAttribute: async (name) => name === 'type' ? 'email' : null,
      fill: async (value) => { observed.email = value }
    }
    const button = {
      isVisible: async () => true,
      isDisabled: async () => false,
      innerText: async () => 'Send invites',
      click: async () => { observed.clicked += 1 }
    }
    const collection = (items) => ({
      count: async () => items.length,
      nth: (index) => items[index]
    })
    const scope = {
      locator: (selector) => collection(selector === 'input' ? [input] : []),
      getByRole: (role, options) => collection(role === 'button' && options.name.test('Send invites') ? [button] : [])
    }

    await submitInviteDialog(scope, 'child@example.test')
    assert.equal(observed.email, 'child@example.test')
    assert.equal(observed.clicked, 1)
  })

  it('clicks the official Remove from workspace confirmation only inside its dialog', async () => {
    const clicked = []
    const button = (label) => ({
      isVisible: async () => true,
      isDisabled: async () => false,
      click: async () => { clicked.push(label) }
    })
    const removeMember = button('Remove member')
    const removeFromWorkspace = button('Remove from workspace')
    const collection = (items) => ({
      count: async () => items.length,
      nth: (index) => items[index]
    })
    const dialog = {
      getByRole: (role, options) => collection(
        role === 'button'
          ? ['Remove member', 'Remove from workspace'].filter((label) => options.name.test(label)).map((label) => label === 'Remove member' ? removeMember : removeFromWorkspace)
          : []
      ),
      getByText: () => collection([])
    }
    const page = {
      locator: (selector) => {
        assert.equal(selector, '[role="dialog"]:visible, [role="alertdialog"]:visible')
        return collection([dialog])
      }
    }

    await confirmOfficialMemberRemoval(page)

    assert.deepEqual(clicked, ['Remove from workspace'])
  })

  it('records Send invites exactly once for workflow continuations', () => {
    const workflow = createWorkflow('member@example.test', 'child@example.test', authURL, 'oauth-session-abcdefghijklmnop', false)
    assert.equal(workflow.inviteSubmitted, false)
    assert.equal(markWorkflowInviteSubmitted(workflow), true)
    const submittedAt = workflow.inviteSubmittedAt
    assert.equal(workflow.inviteSubmitted, true)
    assert.ok(submittedAt > 0)
    assert.equal(markWorkflowInviteSubmitted(workflow), false)
    assert.equal(workflow.inviteSubmittedAt, submittedAt)
  })

  it('keeps existing-account reauthorization out of phone and profile registration', () => {
    const workflow = createReauthorizationWorkflow(
      317,
      'child@example.test',
      'SavedPassword!',
      authURL,
      'oauth-session-abcdefghijklmnop'
    )
    completeReauthorizationOnlyNodes(workflow)

    assert.equal(workflow.mode, 'reauthorization')
    assert.equal(workflow.targetAccountID, 317)
    for (const key of ['members', 'remove', 'invite', 'invite_confirm', 'phone', 'sms_confirm', 'phone_submit', 'sms_poll', 'sms_code', 'profile_wait', 'profile']) {
      assert.equal(workflow.nodes.find((node) => node.key === key)?.status, 'completed')
    }
    assert.equal(workflow.nodes.find((node) => node.key === 'workspace')?.status, 'pending')
  })

  it('resets all reauthorization login nodes after the registration-first reorder', () => {
    const workflow = createReauthorizationWorkflow(
      317,
      'child@example.test',
      'SavedPassword!',
      authURL,
      'oauth-session-abcdefghijklmnop'
    )
    for (const key of ['signup', 'email', 'mail', 'mailbox', 'email_code', 'oauth', 'password']) {
      completeWorkflowNode(workflow, key, '完成')
    }

    resetOAuthWorkflowSteps(workflow)

    for (const key of ['signup', 'email', 'mail', 'mailbox', 'email_code', 'oauth', 'password']) {
      assert.equal(workflow.nodes.find((node) => node.key === key)?.status, 'pending')
    }
    for (const key of ['members', 'remove', 'invite', 'invite_confirm']) {
      assert.equal(workflow.nodes.find((node) => node.key === key)?.status, 'completed')
    }
  })

  it('prefers Send invites when an older Continue action is also present', async () => {
    const clicked = []
    const input = {
      isVisible: async () => true,
      getAttribute: async (name) => name === 'type' ? 'email' : null,
      fill: async () => undefined
    }
    const button = (label) => ({
      isVisible: async () => true,
      isDisabled: async () => false,
      innerText: async () => label,
      click: async () => { clicked.push(label) }
    })
    const collection = (items) => ({
      count: async () => items.length,
      nth: (index) => items[index]
    })
    const scope = {
      locator: (selector) => collection(selector === 'input' ? [input] : []),
      getByRole: (role, options) => {
        if (role !== 'button') return collection([])
        const items = ['Continue', 'Send invites'].filter((label) => options.name.test(label)).map(button)
        return collection(items)
      }
    }

    await submitInviteDialog(scope, 'child@example.test')
    assert.deepEqual(clicked, ['Send invites'])
  })

  it('fills an OAuth verification code against the current browser page', async () => {
    const observed = { code: '', clicked: 0 }
    const input = {
      isVisible: async () => true,
      fill: async (value) => { observed.code = value }
    }
    const button = {
      isVisible: async () => true,
      click: async () => { observed.clicked += 1 }
    }
    const collection = (items) => ({
      count: async () => items.length,
      nth: (index) => items[index]
    })
    const page = {
      locator: (selector) => collection(selector.includes('one-time-code') ? [input] : []),
      getByRole: (role, options) => collection(role === 'button' && options.name.test('Continue') ? [button] : [])
    }

    await fillVerificationCode(page, '123456')
    assert.equal(observed.code, '123456')
    assert.equal(observed.clicked, 1)
  })

  it('returns from an old SMS page and switches a password prompt to email-code login', async () => {
    let state = 'sms_code'
    let backClicks = 0
    let codeOptionClicks = 0
    const collection = (items) => ({
      count: async () => items.length,
      nth: (index) => items[index],
      allTextContents: async () => []
    })
    const input = (type, autocomplete = '') => ({
      isVisible: async () => true,
      getAttribute: async (name) => ({ type, autocomplete }[name] || null),
      fill: async () => undefined
    })
    const page = {
      locator: (selector) => {
        if (selector === 'body') {
          return { innerText: async () => state === 'sms_code'
            ? 'Check your phone SMS code'
            : state === 'password' ? 'Enter your password' : 'Check your inbox for a verification code' }
        }
        const currentInput = state === 'sms_code'
          ? input('text', 'one-time-code')
          : state === 'password' ? input('password') : input('text', 'one-time-code')
        if (selector === 'input') return collection([currentInput])
        if (selector.includes('one-time-code')) return collection(['sms_code', 'email_code'].includes(state) ? [currentInput] : [])
        return collection([])
      },
      getByRole: (role, options) => collection(
        role === 'button' && state === 'password' && options.name.test('Continue with email code')
          ? [{ isVisible: async () => true, click: async () => { codeOptionClicks += 1; state = 'email_code' } }]
          : []
      ),
      goBack: async () => { backClicks += 1; state = 'password' },
      goto: async () => undefined,
      url: () => 'https://auth.openai.com/phone-verification'
    }
    const workflow = createWorkflow('member@example.test', 'child@example.test', authURL, 'oauth-session-abcdefghijklmnop', false)

    assert.equal(await recoverOpenAIPhoneEntry(page, workflow), 'email_code')
    assert.equal(backClicks, 1)
    assert.equal(codeOptionClicks, 1)
  })

  it('restarts the XIASS official PKCE URL and submits the mailbox when the authorization step expires', async () => {
    let state = 'invalid_auth_step'
    let submittedEmail = ''
    let backClicks = 0
    const visited = []
    const collection = (items) => ({
      count: async () => items.length,
      nth: (index) => items[index],
      allTextContents: async () => []
    })
    const emailInput = {
      isVisible: async () => true,
      getAttribute: async (name) => ({ type: 'email', autocomplete: 'username' }[name] || null),
      fill: async (value) => { submittedEmail = value }
    }
    const codeInput = {
      isVisible: async () => true,
      getAttribute: async (name) => ({ type: 'text', autocomplete: 'one-time-code' }[name] || null),
      fill: async () => undefined
    }
    const page = {
      locator: (selector) => {
        if (selector === 'body') {
          return { innerText: async () => state === 'invalid_auth_step'
            ? 'Invalid authorization step. error_code: invalid_auth_step'
            : state === 'email' ? 'Welcome back Email address' : 'Check your inbox for a verification code' }
        }
        if (selector === 'input') {
          if (state === 'email') return collection([emailInput])
          if (state === 'email_code') return collection([codeInput])
        }
        if (selector.includes('one-time-code')) return collection(state === 'email_code' ? [codeInput] : [])
        return collection([])
      },
      getByRole: (role, options) => collection(
        role === 'button' && state === 'email' && options.name.test('Continue')
          ? [{ isVisible: async () => true, click: async () => { state = 'email_code' } }]
          : []
      ),
      goBack: async () => { backClicks += 1 },
      goto: async (url) => { visited.push(url); state = 'email' },
      url: () => 'https://auth.openai.com/add-phone'
    }
    const workflow = createWorkflow('member@example.test', 'child@example.test', authURL, 'oauth-session-abcdefghijklmnop', false)

    assert.equal(await recoverOpenAIPhoneEntry(page, workflow), 'email_code')
    assert.deepEqual(visited, [authURL])
    assert.equal(backClicks, 0)
    assert.equal(submittedEmail, 'child@example.test')
  })

  it('recovers the matching localhost callback from Chromium navigation history', () => {
    const matching = 'http://localhost:1455/auth/callback?code=secret-code&state=current-state'
    const result = callbackURLFromNavigationEntries([
      { url: 'https://auth.openai.com/sign-in-with-chatgpt/codex/consent' },
      { url: 'http://localhost:1455/auth/callback?code=old-code&state=old-state' },
      { url: matching }
    ], 'current-state')
    assert.equal(result, matching)
    assert.equal(callbackURLFromNavigationEntries([{ url: matching }], 'other-state'), '')
  })

  it('attributes a long-running action failure to its actual active node', () => {
    const workflow = createWorkflow('member@example.test', 'child@example.test', authURL, 'oauth-session-abcdefghijklmnop', false)
    completeWorkflowNode(workflow, 'sms_code', '短信验证码已提交')
    setWorkflowNode(workflow, 'callback', 'running', '正在捕获回调')
    assert.equal(workflowFailureNodeKey(workflow, 'sms_code'), 'callback')
  })

  it('cancels a running workflow and clears its short-lived login secrets', () => {
    const workflow = createWorkflow('member@example.test', 'child@example.test', authURL, 'oauth-session-abcdefghijklmnop', false)
    workflow.loginPassword = 'SavedPassword!'
    setWorkflowNode(workflow, 'invite', 'running', '正在提交邀请')

    cancelWorkflowState(workflow)

    assert.equal(workflow.status, 'cancelled')
    assert.equal(workflow.cancelRequested, true)
    assert.equal(workflow.loginPassword, '')
    assert.equal(workflow.nodes.find((node) => node.key === 'invite').status, 'cancelled')
  })

  it('persists a manual SMS pause and resumes without clearing email-code state', () => {
    const workflow = createWorkflow('member@example.test', 'child@example.test', authURL, 'oauth-session-abcdefghijklmnop', false)
    workflow.emailCodeGeneration = 2
    workflow.emailCodePurpose = 'oauth_login'
    completeWorkflowNode(workflow, 'phone_submit', '手机号已提交')
    setWorkflowNode(workflow, 'sms_poll', 'waiting', '等待短信')
    workflow.status = 'manual_required'

    pauseWorkflowState(workflow)
    assert.equal(workflow.status, 'paused')
    assert.equal(workflowSummary(workflow).pause_requested, true)
    assert.equal(workflow.emailCodeGeneration, 2)
    assert.equal(workflow.emailCodePurpose, 'oauth_login')

    const resumed = resumePausedWorkflow(workflow)
    assert.equal(resumed.status, 'manual_required')
    assert.equal(resumed.pause_requested, false)
    assert.equal(workflow.emailCodeGeneration, 2)
    assert.equal(workflow.emailCodePurpose, 'oauth_login')
  })

  it('marks a running browser node paused immediately and blocks its next node', () => {
    const workflow = createWorkflow('member@example.test', 'child@example.test', authURL, 'oauth-session-abcdefghijklmnop', false)
    setWorkflowNode(workflow, 'invite', 'running', '正在邀请')

    pauseWorkflowState(workflow)

    assert.equal(workflow.status, 'paused')
    assert.equal(workflow.pauseRequested, true)
    assert.throws(() => setWorkflowNode(workflow, 'invite_confirm', 'running', '正在确认'), /工作流已暂停/)
    assert.equal(workflow.currentNodeKey, 'invite_confirm')
  })

  it('keeps the complete node order stable', () => {
    assert.deepEqual(workflowNodeDefinitions.map(([key]) => key), [
      'signup', 'email', 'mail', 'mailbox', 'email_code', 'members', 'remove', 'invite', 'invite_confirm',
      'oauth', 'password', 'phone', 'sms_confirm', 'phone_submit',
      'sms_poll', 'sms_code', 'profile_wait', 'profile', 'workspace_wait',
      'workspace', 'callback', 'import'
    ])
  })

  it('does not fabricate a password for an email-code registration', () => {
    const workflow = createWorkflow('member@example.test', 'child@example.test', authURL, 'oauth-session-abcdefghijklmnop', false)
    assert.equal('generatedPassword' in workflow, false)
    assert.equal(workflowSummary(workflow).password_available, false)
    assert.equal(workflowSummary(workflow).nodes.length, 22)
  })

  it('encrypts persisted reauthorization secrets with authenticated ciphertext', () => {
    const password = 'SavedPassword!'
    const plaintext = JSON.stringify({ schema_version: 4, workflows: [{ loginPassword: password }] })
    const encrypted = encryptWorkflowState(plaintext)
    assert.equal(encrypted.includes(password), false)
    assert.equal(decryptWorkflowState(encrypted), plaintext)

    const tampered = JSON.parse(encrypted)
    tampered.ciphertext = `${tampered.ciphertext.slice(0, -1)}${tampered.ciphertext.endsWith('A') ? 'B' : 'A'}`
    assert.throws(() => decryptWorkflowState(JSON.stringify(tampered)))
  })

  it('reports only the current node and never completes the SMS gate implicitly', () => {
    const workflow = createWorkflow('member@example.test', 'child@example.test', authURL, 'oauth-session-abcdefghijklmnop', false)
    completeWorkflowNode(workflow, 'phone', '手机号页面已出现')
    setWorkflowNode(workflow, 'sms_confirm', 'waiting', '等待站内确认')
    workflow.status = 'manual_required'
    const summary = workflowSummary(workflow)
    assert.equal(summary.current_node, 'sms_confirm')
    assert.equal(summary.nodes.find((node) => node.key === 'sms_confirm').status, 'waiting')
    assert.equal(summary.nodes.find((node) => node.key === 'phone_submit').status, 'pending')
  })

  it('validates OAuth session and code inputs without retaining them', () => {
    assert.equal(validateOAuthSessionID('oauth-session-abcdefghijklmnop'), 'oauth-session-abcdefghijklmnop')
    assert.equal(validateWorkflowCode(' 123456 '), '123456')
    assert.throws(() => validateOAuthSessionID('short'))
    assert.throws(() => validateWorkflowCode('12'))
    assert.throws(() => validateWorkflowCode('secret-code'))
  })
})
