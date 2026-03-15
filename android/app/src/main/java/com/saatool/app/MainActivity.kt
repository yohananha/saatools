package com.saatool.app

import android.app.Activity
import android.content.Intent
import android.net.Uri
import android.os.Bundle
import android.os.Environment
import android.provider.DocumentsContract
import android.util.Base64
import android.util.Log
import android.view.WindowManager
import android.webkit.JavascriptInterface
import android.webkit.ValueCallback
import android.webkit.WebChromeClient
import android.webkit.WebResourceRequest
import android.webkit.WebView
import android.webkit.WebViewClient
import androidx.appcompat.app.AlertDialog
import androidx.appcompat.app.AppCompatActivity
import androidx.core.view.WindowInsetsCompat
import androidx.core.view.WindowInsetsControllerCompat
import java.io.File

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

        // Enable Chrome DevTools remote debugging for debug builds only.
        // Connect via chrome://inspect on a desktop browser while USB-connected.
        val isDebuggable = (applicationInfo.flags and android.content.pm.ApplicationInfo.FLAG_DEBUGGABLE) != 0
        WebView.setWebContentsDebuggingEnabled(isDebuggable)

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
        } else if (requestCode == FOLDER_PICKER_REQUEST) {
            if (resultCode == Activity.RESULT_OK && data?.data != null) {
                val treeUri = data.data!!
                try {
                    contentResolver.takePersistableUriPermission(treeUri,
                        Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_GRANT_WRITE_URI_PERMISSION)
                } catch (_: SecurityException) { }
                val path = treeUriToPath(treeUri)
                if (path != null) {
                    val escaped = path.replace("\\", "\\\\").replace("'", "\\'")
                    webView.evaluateJavascript("typeof window.onFolderChosen === 'function' && window.onFolderChosen('$escaped');", null)
                } else {
                    runOnUiThread {
                        AlertDialog.Builder(this)
                            .setMessage("This folder cannot be used. Please choose Downloads or App storage below.")
                            .setPositiveButton(android.R.string.ok) { _, _ ->
                                showFolderPresetsFallback()
                            }
                            .setNegativeButton(android.R.string.cancel, null)
                            .show()
                    }
                }
            }
        }
        @Suppress("DEPRECATION")
        super.onActivityResult(requestCode, resultCode, data)
    }

    private fun treeUriToPath(uri: Uri): String? {
        if (uri.scheme == "file") return uri.path
        if (uri.scheme != "content") return null
        val docId = DocumentsContract.getTreeDocumentId(uri) ?: return null
        if (docId.startsWith("primary:")) {
            val rel = docId.removePrefix("primary:")
            val root = Environment.getExternalStorageDirectory().absolutePath
            val path = File(root, rel).absolutePath
            return if (File(path).exists() || File(path).mkdirs()) path else null
        }
        // Try secondary storage (e.g. SD card): document ID is often "volumeName:relativePath"
        val colon = docId.indexOf(':')
        if (colon > 0) {
            val volume = docId.substring(0, colon)
            val rel = docId.substring(colon + 1)
            val storageRoot = File("/storage", volume)
            if (storageRoot.exists()) {
                val path = File(storageRoot, rel).absolutePath
                return if (File(path).exists() || File(path).mkdirs()) path else null
            }
        }
        return null
    }

    private fun showFolderPresetsFallback() {
        val downloadsPath = publicDownloadsPath()
        val appPath = applicationContext.getExternalFilesDir(Environment.DIRECTORY_DOWNLOADS)?.absolutePath
            ?: applicationContext.filesDir.absolutePath
        val items = mutableListOf<Pair<String, String>>()
        if (downloadsPath != null) items.add("Downloads" to downloadsPath)
        items.add("App storage" to appPath)
        val labels = items.map { it.first }.toTypedArray()
        val paths = items.map { it.second }.toTypedArray()
        AlertDialog.Builder(this)
            .setTitle("Choose folder for books")
            .setItems(labels) { _, which ->
                val path = paths.getOrNull(which) ?: return@setItems
                val escaped = path.replace("\\", "\\\\").replace("'", "\\'")
                webView.evaluateJavascript("typeof window.onFolderChosen === 'function' && window.onFolderChosen('$escaped');", null)
            }
            .setNegativeButton(android.R.string.cancel, null)
            .show()
    }

    private fun publicDownloadsPath(): String? {
        val dir = Environment.getExternalStoragePublicDirectory(Environment.DIRECTORY_DOWNLOADS)
        val file = File(dir.absolutePath)
        return when {
            file.exists() && file.canWrite() -> file.absolutePath
            !file.exists() && file.mkdirs() && file.canWrite() -> file.absolutePath
            else -> null
        }
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

        /** Opens the system file manager to choose a folder (like Import). Calls window.onFolderChosen(path) with the chosen path, or shows preset options if the chosen folder cannot be used. */
        @JavascriptInterface
        fun showFolderPresets() {
            runOnUiThread {
                val intent = Intent(Intent.ACTION_OPEN_DOCUMENT_TREE)
                intent.addFlags(Intent.FLAG_GRANT_PERSISTABLE_URI_PERMISSION or Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_GRANT_WRITE_URI_PERMISSION)
                @Suppress("DEPRECATION")
                startActivityForResult(intent, FOLDER_PICKER_REQUEST)
            }
        }

        /** Writes base64-encoded content to a file at the given path. Returns "ok" on success or an error message. Used so EPUB/TXT export can persist files on Android (WebView does not handle &lt;a download&gt;). */
        @JavascriptInterface
        fun saveFile(fullPath: String, base64Content: String): String {
            return try {
                val bytes = Base64.decode(base64Content, Base64.DEFAULT)
                val file = File(fullPath)
                file.parentFile?.mkdirs()
                file.writeBytes(bytes)
                "ok"
            } catch (e: Exception) {
                Log.e(TAG, "saveFile failed: $fullPath", e)
                e.message ?: "Write failed"
            }
        }
    }

    companion object {
        private const val TAG = "MainActivity"
        private const val FILE_CHOOSER_REQUEST = 1001
        private const val FOLDER_PICKER_REQUEST = 1002
    }
}
