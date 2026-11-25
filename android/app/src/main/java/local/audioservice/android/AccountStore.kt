package local.audioservice.android

import android.content.Context
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import android.util.Base64
import org.json.JSONObject
import java.io.IOException
import java.security.KeyStore
import java.security.MessageDigest
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

internal data class Profile(val base: String, val id: String, val displayName: String) {
    fun scope(): String = MessageDigest.getInstance("SHA-256")
        .digest("$base\u0000$id".toByteArray(Charsets.UTF_8))
        .joinToString("") { "%02x".format(it) }
}

internal class AccountStore(context: Context) {
    private val preferences = context.getSharedPreferences("account", Context.MODE_PRIVATE)
    private val alias = "audioservice-client-key"

    fun active(): Profile? = runCatching {
        val raw = preferences.getString("profile", null) ?: return null
        val data = JSONObject(raw)
        Profile(data.getString("base"), data.getString("id"), data.getString("display_name"))
    }.getOrNull()

    fun credential(): String? = runCatching {
        val iv = preferences.getString("iv", null) ?: return null
        val data = preferences.getString("credential", null) ?: return null
        val cipher = Cipher.getInstance("AES/GCM/NoPadding")
        cipher.init(Cipher.DECRYPT_MODE, keystoreKey(), GCMParameterSpec(128, Base64.decode(iv, Base64.NO_WRAP)))
        cipher.doFinal(Base64.decode(data, Base64.NO_WRAP)).toString(Charsets.UTF_8)
    }.getOrNull()

    fun setActive(profile: Profile, credential: String) {
        val cipher = Cipher.getInstance("AES/GCM/NoPadding")
        cipher.init(Cipher.ENCRYPT_MODE, keystoreKey())
        val encrypted = cipher.doFinal(credential.toByteArray(Charsets.UTF_8))
        val data = JSONObject().put("base", profile.base).put("id", profile.id).put("display_name", profile.displayName)
        val saved = preferences.edit()
            .putString("profile", data.toString())
            .putString("iv", Base64.encodeToString(cipher.iv, Base64.NO_WRAP))
            .putString("credential", Base64.encodeToString(encrypted, Base64.NO_WRAP))
            .commit()
        if (!saved) throw IOException("Не удалось сохранить аккаунт.")
    }

    fun clear() { preferences.edit().clear().apply() }

    private fun keystoreKey(): SecretKey {
        val store = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
        val existing = store.getKey(alias, null)
        if (existing is SecretKey) return existing
        val generator = KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, "AndroidKeyStore")
        val spec = KeyGenParameterSpec.Builder(alias, KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT)
            .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
            .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
            .build()
        generator.init(spec)
        return generator.generateKey()
    }
}
