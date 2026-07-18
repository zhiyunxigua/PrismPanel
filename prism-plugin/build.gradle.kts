plugins {
    id("com.gradleup.shadow") version "8.3.6" apply false
}

allprojects {
    group = "com.xigua.prism"
    version = "0.2.0"

    repositories {
        mavenLocal()
        mavenCentral()
        maven("https://repo.papermc.io/repository/maven-public/")
        maven("https://hub.spigotmc.org/nexus/content/repositories/snapshots/")
        maven("https://oss.sonatype.org/content/repositories/snapshots/")
    }
}

subprojects {
    apply(plugin = "java-library")

    extensions.configure<JavaPluginExtension> {
        toolchain.languageVersion.set(JavaLanguageVersion.of(17))
    }

    tasks.withType<JavaCompile>().configureEach {
        options.encoding = "UTF-8"
        options.release.set(17)
    }

    tasks.withType<Test>().configureEach {
        useJUnitPlatform()
    }
}
