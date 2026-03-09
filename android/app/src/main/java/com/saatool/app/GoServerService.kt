package com.saatool.app

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.Service
import android.content.Intent
import android.os.IBinder
import android.util.Log
import mobile.Mobile

/**
 * GoServerService — foreground service that runs the Go HTTP server.
 *
 * Started by MainActivity on launch; keeps the Go goroutines alive
 * even when the WebView is paused. The notification is required by Android
 * for all foreground services.
 */
class GoServerService : Service() {

    private var goServer: mobile.Server? = null

    override fun onCreate() {
        super.onCreate()
        createNotificationChannel()
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        startForeground(NOTIFICATION_ID, buildNotification())

        val filesDir = applicationContext.filesDir.absolutePath
        Thread {
            try {
                Log.i(TAG, "Starting Go server in $filesDir")
                goServer = Mobile.start(SERVER_PORT.toLong(), filesDir)
                Log.i(TAG, "Go server started on port $SERVER_PORT")
            } catch (e: Exception) {
                Log.e(TAG, "Go server failed to start", e)
            }
        }.start()

        return START_STICKY
    }

    override fun onDestroy() {
        try {
            goServer?.stop()
            Log.i(TAG, "Go server stopped")
        } catch (e: Exception) {
            Log.e(TAG, "Error stopping Go server", e)
        }
        super.onDestroy()
    }

    override fun onBind(intent: Intent?): IBinder? = null

    private fun createNotificationChannel() {
        val channel = NotificationChannel(
            CHANNEL_ID,
            "SaaTool Server",
            NotificationManager.IMPORTANCE_LOW
        ).apply {
            description = "Keeps the translation server running"
        }
        val manager = getSystemService(NOTIFICATION_SERVICE) as NotificationManager
        manager.createNotificationChannel(channel)
    }

    private fun buildNotification(): Notification =
        Notification.Builder(this, CHANNEL_ID)
            .setContentTitle("SaaTool")
            .setContentText("Translation server running")
            .setSmallIcon(android.R.drawable.ic_menu_info_details)
            .build()

    companion object {
        private const val TAG = "GoServerService"
        const val SERVER_PORT = 8766
        private const val NOTIFICATION_ID = 1
        private const val CHANNEL_ID = "saatool_server"
    }
}
