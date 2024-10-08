// Configuration for your app
// https://quasar.dev/quasar-cli/quasar-conf-js

const { configure } = require('quasar/wrappers')

module.exports = configure(function (ctx) {
  return {
    eslint: {
      warnings: true,
      errors: true
    },

    // app boot file (/src/boot)
    // --> boot files are part of "main.js"
    boot: [
      'api'
    ],

    css: [
      'sass/app.scss'
    ],

    extras: [
      // 'ionicons-v4',
      // 'mdi-v3',
      // 'fontawesome-v5',
      'eva-icons',
      // 'themify',
      // 'roboto-font-latin-ext', // this or either 'roboto-font', NEVER both!

      'roboto-font', // optional, you are not bound to it
      'material-icons' // optional, you are not bound to it
    ],

    framework: {
      // iconSet: 'ionicons-v4',
      // lang: 'de', // Quasar language

      // all: true, // --- includes everything; for dev only!

      components: [
        'QLayout',
        'QHeader',
        'QFooter',
        'QDrawer',
        'QPageContainer',
        'QPage',
        'QPageSticky',
        'QPageScroller',
        'QToolbar',
        'QSpace',
        'QToolbarTitle',
        'QTooltip',
        'QBtn',
        'QIcon',
        'QList',
        'QItem',
        'QExpansionItem',
        'QItemSection',
        'QItemLabel',
        'QTabs',
        'QTab',
        'QRouteTab',
        'QAvatar',
        'QSeparator',
        'QScrollArea',
        'QImg',
        'QBadge',
        'QCard',
        'QCardSection',
        'QCardActions',
        'QBreadcrumbs',
        'QBreadcrumbsEl',
        'QInput',
        'QToggle',
        'QForm',
        'QField',
        'QSelect',
        'QCheckbox',
        'QRadio',
        'QMenu',
        'QAjaxBar',
        'QTable',
        'QTh',
        'QTr',
        'QTd',
        'QFab',
        'QFabAction',
        'QDialog',
        'QUploader',
        'QTree',
        'QChip',
        'QBtnToggle'
      ],

      directives: [
        'ClosePopup',
        'Ripple'
      ],

      // Quasar plugins
      plugins: [
        'Notify',
        'Dialog',
        'LoadingBar'
      ],

      config: {
        notify: { /* Notify defaults */ },
        loadingBar: {
          position: 'top',
          color: 'accent',
          size: '2px'
        }
      }
    },

    supportIE: false,

    build: {
      // Needed to have relative assets in the index.html
      // https://github.com/quasarframework/quasar/issues/8513#issuecomment-1127654470
      extendViteConf (viteConf, { isServer, isClient }) {
        viteConf.base = ''
      },
      viteVuePluginOptions: {
        template: {
          compilerOptions: {
            isCustomElement: (tag) => tag.startsWith('hub-')
          }
        }
      },
      target: {
        browser: ['edge88', 'firefox78', 'chrome87', 'safari13.1'],
        node: 'node20'
      },
      publicPath: process.env.APP_PUBLIC_PATH || '',
      env: process.env.APP_ENV === 'development'
        ? { // staging:
            APP_ENV: process.env.APP_ENV,
            APP_API: process.env.APP_API || '/api'
          }
        : { // production:
            APP_ENV: process.env.APP_ENV,
            APP_API: process.env.APP_API || '/api'
          },
      uglifyOptions: {
        compress: {
          drop_console: process.env.APP_ENV === 'production',
          drop_debugger: process.env.APP_ENV === 'production'
        }
      },
      scopeHoisting: true,
      vueRouterMode: 'hash' // available values: 'hash', 'history'
    },

    devServer: {
      // https: true,
      port: 8081,
      open: true, // opens browser window automatically
      proxy: {
        // proxy all API requests to real Traefik
        '/api': {
          target: 'http://localhost:8080',
          changeOrigin: true
        }
      }
    },

    // animations: 'all', // --- includes all animations
    animations: [],

    ssr: {
      pwa: false
    },

    pwa: {

      workboxMode: 'injectManifest', // or 'generateSW'
      // workboxPluginMode: 'InjectManifest',
      // workboxOptions: {}, // only for NON InjectManifest
      workboxOptions: {
        skipWaiting: true,
        clientsClaim: true
      },

      chainWebpackCustomSW (chain) {
        chain.plugin('eslint-webpack-plugin')
          .use(ESLintPlugin, [{ extensions: ['js'] }])
      },

      manifest: {
        // name: 'Traefik',
        // short_name: 'Traefik',
        // description: 'Traefik UI',
        display: 'standalone',
        orientation: 'portrait',
        background_color: '#ffffff',
        theme_color: '#027be3',
        icons: [
          {
            src: 'icons/icon-128x128.png',
            sizes: '128x128',
            type: 'image/png'
          },
          {
            src: 'icons/icon-192x192.png',
            sizes: '192x192',
            type: 'image/png'
          },
          {
            src: 'icons/icon-256x256.png',
            sizes: '256x256',
            type: 'image/png'
          },
          {
            src: 'icons/icon-384x384.png',
            sizes: '384x384',
            type: 'image/png'
          },
          {
            src: 'icons/icon-512x512.png',
            sizes: '512x512',
            type: 'image/png'
          }
        ]
      }
    },

    cordova: {
      // id: 'us.containo.traefik',
      // noIosLegacyBuildFlag: true, // uncomment only if you know what you are doing
    },

    electron: {
      // bundler: 'builder', // or 'packager'

      extendWebpack (cfg) {
        // do something with Electron main process Webpack cfg
        // chainWebpack also available besides this extendWebpack
      },

      packager: {
        // https://github.com/electron-userland/electron-packager/blob/master/docs/api.md#options

        // OS X / Mac App Store
        // appBundleId: '',
        // appCategoryType: '',
        // osxSign: '',
        // protocol: 'myapp://path',

        // Windows only
        // win32metadata: { ... }
      },

      builder: {
        // https://www.electron.build/configuration/configuration

        // appId: 'traefik-ui'
      }
    }
  }
})
