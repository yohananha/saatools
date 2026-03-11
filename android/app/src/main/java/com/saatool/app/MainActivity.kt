package com.saatool.app

import android.app.Activity
import android.content.Intent
import android.net.Uri
import android.os.Bundle
import android.util.Log
import android.view.WindowManager
import android.webkit.JavascriptInterface
import android.webkit.ValueCallback
import android.webkit.WebChromeClient
import android.webkit.WebResourceRequest
import android.webkit.WebView
import android.webkit.WebViewClient
import androidx.appcompat.app.AppCompatActivity
import androidx.core.view.WindowInsetsCompat
import androidx.core.view.WindowInsetsControllerCompat

/**
 * MainActivity — hosts a full-screen WebView that loads the React frontend
 * from the local Go HTTP server at http://localhost:8766.
 *
 * WebChromeClient.onShowFileChooser is required to make <input type="file">
 * work inside a WebView — without it the file picker silently does nothing.
 */
class MainActivity : AppCompatActivity() {

    private lateinit var webView: WebView
    private var filePathCallback: ValueCallback<Array<Uri>>? = null

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)

        // Hide the navigation bar — gives the WebView the full screen height.
        // Swiping up from the bottom edge reveals it temporarily.
        WindowInsetsControllerCompat(window, window.decorView).apply {
            hide(WindowInsetsCompat.Type.navigationBars())
            systemBarsBehavior =
                WindowInsetsControllerCompat.BEHAVIOR_SHOW_TRANSIENT_BARS_BY_SWIPE
        }

        // Start the Go server service before setting up the WebView.
        startForegroundService(Intent(this, GoServerService::class.java))

        webView = WebView(this).apply {
            settings.apply {
                javaScriptEnabled = true
                domStorageEnabled = true
                allowFileAccess = false
                allowContentAccess = false
                setSupportZoom(false)
                builtInZoomControls = false
                displayZoomControls = false
            }

            webViewClient = object : WebViewClient() {
                override fun shouldOverrideUrlLoading(
                    view: WebView,
                    request: WebResourceRequest
                ): Boolean {
                    val url = request.url.toString()
                    return !url.startsWith("http://localhost:${GoServerService.SERVER_PORT}")
                }
            }

            addJavascriptInterface(WebAppInterface(), "AndroidBridge")

            // Required to make <input type="file"> work in WebView.
            webChromeClient = object : WebChromeClient() {
                override fun onShowFileChooser(
                    webView: WebView,
                    callback: ValueCallback<Array<Uri>>,
                    params: FileChooserParams
                ): Boolean {
                    // Cancel any pending callback first.
                    filePathCallback?.onReceiveValue(null)
                    filePathCallback = callback

                    val intent = params.createIntent()
                    startActivityForResult(intent, FILE_CHOOSER_REQUEST)
                    return true
                }
            }
        }

        setContentView(webView)

        // Give the Go server ~1.5 s to bind to the port, then load the app.
        webView.postDelayed({
            Log.i(TAG, "Loading http://localhost:${GoServerService.SERVER_PORT}/")
            webView.loadUrl("http://localhost:${GoServerService.SERVER_PORT}/")
        }, 1500)
    }

    @Suppress("OVERRIDE_DEPRECATION")
    override fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?) {
        if (requestCode == FILE_CHOOSER_REQUEST) {
            val results = if (resultCode == Activity.RESULT_OK)
                WebChromeClient.FileChooserParams.parseResult(resultCode, data)
            else null
            filePathCallback?.onReceiveValue(results)
            filePathCallback = null
        }
        @Suppress("DEPRECATION")
        super.onActivityResult(requestCode, resultCode, data)
    }

    @Suppress("OVERRIDE_DEPRECATION")
    override fun onBackPressed() {
        if (webView.canGoBack()) webView.goBack()
        else @Suppress("DEPRECATION") super.onBackPressed()
    }

    inner class WebAppInterface {
        @JavascriptInterface
        fun setKeepScreenOn(on: Boolean) {
            runOnUiThread {
                if (on) window.addFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)
                else    window.clearFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)
            }
        }
    }

    companion object {
        private const val TAG = "MainActivity"
        private const val FILE_CHOOSER_REQUEST = 1001
    }
}
