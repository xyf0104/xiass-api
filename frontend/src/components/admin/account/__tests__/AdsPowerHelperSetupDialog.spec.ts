import { mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'
import AdsPowerHelperSetupDialog from '../AdsPowerHelperSetupDialog.vue'

const BaseDialogStub = {
  props: ['show', 'title'],
  emits: ['close'],
  template: '<section v-if="show"><h2>{{ title }}</h2><slot /></section>',
}

describe('AdsPowerHelperSetupDialog', () => {
  afterEach(() => vi.restoreAllMocks())

  it('provides fixed macOS and Windows downloads and opens the local setup page', async () => {
    const popup = { opener: window }
    const open = vi.spyOn(window, 'open').mockReturnValue(popup as unknown as Window)
    const wrapper = mount(AdsPowerHelperSetupDialog, {
      props: { show: true, serverOrigin: 'https://api2.example.test', environmentKey: 'api2' },
      global: { stubs: { BaseDialog: BaseDialogStub, Icon: true } },
    })

    expect(wrapper.get('[data-testid="adspower-helper-download-macos"]').attributes('href')).toBe('https://github.com/xyf0104/xiass-api/releases/download/adspower-helper-latest/xiass-adspower-helper-macos-universal.dmg')
    expect(wrapper.get('[data-testid="adspower-helper-download-windows"]').attributes('href')).toBe('https://github.com/xyf0104/xiass-api/releases/download/adspower-helper-latest/xiass-adspower-helper-windows-x64.exe')
    await wrapper.get('[data-testid="open-adspower-helper-setup"]').trigger('click')

    expect(open).toHaveBeenCalledWith('http://127.0.0.1:34987/setup?server=https%3A%2F%2Fapi2.example.test&environment=api2', '_blank')
    expect(popup.opener).toBeNull()
  })
})
